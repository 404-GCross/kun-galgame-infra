// user_playtime.go — the playtime report face's service half: what a Galgame
// manager writes when its user finishes a session, and what it reads back on
// the next device.
//
// The write is an UPSERT of an ABSOLUTE total keyed by (user, work, client).
// Nothing here accumulates: a client that re-sends the same number a hundred
// times leaves the row exactly as the first send did, which is the property
// that makes this face safe to retry, safe to run on a flaky connection, and
// safe to point at from an app that has no idea what it sent last time.
//
// A report is never a permission to edit anything. This service touches
// catalog_user_playtime and nothing else — the public aggregate is produced by
// internal/jobs/userplaytime on its own schedule, so no user's write can move
// a published number synchronously.
package service

import (
	"context"
	stderrors "errors"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The refusals of the playtime face.
var (
	// ErrPlaytimeActorRequired: a playtime belongs to somebody. There is no
	// system-attributed report, the way there is no system-attributed vote.
	ErrPlaytimeActorRequired = stderrors.New("catalog: a playtime report requires a reporting user")
	// ErrPlaytimeClientRequired: the reporting application's identity is the
	// third key member and the handle a bad client is excluded by, so a report
	// that cannot name one cannot be stored.
	ErrPlaytimeClientRequired = stderrors.New("catalog: a playtime report requires a client id")
	// ErrPlaytimeWorkUnavailable: no such work, soft-deleted, or not live. A
	// merged-away id lands here too — report against the survivor.
	ErrPlaytimeWorkUnavailable = stderrors.New("catalog: work not available for playtime reporting")
	// ErrPlaytimeMinutesRange: negative, or past the 1000-hour ceiling.
	ErrPlaytimeMinutesRange = stderrors.New("catalog: minutes must be between 0 and 60000")
	// ErrPlaytimeBadStatus: a status word outside the four the face knows.
	ErrPlaytimeBadStatus = stderrors.New("catalog: unknown playtime status")
	// ErrPlaytimeUnknownSource: the ref's source key is not in the registry.
	ErrPlaytimeUnknownSource = stderrors.New("catalog: unknown external source key")
	// ErrPlaytimeRefUnresolved: the source is known but nothing in the catalog
	// is anchored to that id — the caller's game is not (yet) a work here.
	ErrPlaytimeRefUnresolved = stderrors.New("catalog: no work is anchored to that external id")
)

// UserPlaytimeService owns the per-user playtime lane.
type UserPlaytimeService struct{ db *gorm.DB }

func NewUserPlaytimeService(db *gorm.DB) *UserPlaytimeService {
	return &UserPlaytimeService{db: db}
}

// PlaytimeReport is one client's statement about one user's time on one work.
// ActorUID / ClientID / Site are all resolved from the verified token by the
// handler; the wire supplies only the three value fields.
type PlaytimeReport struct {
	ActorUID     int64
	WorkID       int64
	ClientID     string
	Site         string
	Minutes      int
	Status       int16
	LastPlayedAt *time.Time
}

// PlaytimeRecord is one stored report, as the read faces hand it back.
type PlaytimeRecord struct {
	WorkID       int64
	Minutes      int
	Status       int16
	LastPlayedAt *time.Time
	ClientID     string
	UpdatedAt    time.Time
}

// Report upserts one report and returns the row as stored.
//
// The conflict target is the full grain (actor_uid, work_id, client_id): a
// second call from the same app overwrites, a call from a DIFFERENT app of the
// same user inserts alongside. That is the design's core — see the model's
// type doc for why two managers are two measurements rather than a conflict to
// resolve at write time.
func (s *UserPlaytimeService) Report(ctx context.Context, r PlaytimeReport) (*PlaytimeRecord, error) {
	if err := validateReport(r); err != nil {
		return nil, err
	}
	row := model.CatalogUserPlaytime{
		ActorUID: r.ActorUID, WorkID: r.WorkID, ClientID: r.ClientID,
		Minutes: r.Minutes, Status: r.Status, LastPlayedAt: r.LastPlayedAt, Site: r.Site,
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertReportableWork(tx, r.WorkID); err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "actor_uid"}, {Name: "work_id"}, {Name: "client_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"minutes":        r.Minutes,
				"status":         r.Status,
				"last_played_at": r.LastPlayedAt,
				"site":           r.Site,
				"updated_at":     time.Now(),
			}),
		}).Create(&row).Error
	})
	if err != nil {
		return nil, err
	}
	return &PlaytimeRecord{
		WorkID: row.WorkID, Minutes: row.Minutes, Status: row.Status,
		LastPlayedAt: row.LastPlayedAt, ClientID: row.ClientID, UpdatedAt: row.UpdatedAt,
	}, nil
}

// ResolveRef turns (source key, external id) into a catalog work id.
//
// This is the half of the design that decides whether the face is adoptable at
// all: a manager holds a VNDB id or a DLsite workno, never our work id, and
// requiring it to hold ours would push a mapping problem onto every app author
// who wants to call this once. Only EXACT anchors resolve — a probable link is
// a research lead, not an identity, and attributing somebody's playtime through
// one would silently file it under the wrong game.
func (s *UserPlaytimeService) ResolveRef(ctx context.Context, sourceKey, externalID string) (int64, error) {
	var sourceID int16
	err := s.db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_source WHERE key = ?`, sourceKey).Scan(&sourceID).Error
	if err != nil {
		return 0, err
	}
	if sourceID == 0 {
		return 0, ErrPlaytimeUnknownSource
	}
	var workID int64
	// Newest anchor wins if a source somehow holds two exact rows for one id;
	// in practice the (source, external_id, entity_type) uniqueness makes this
	// a single row.
	err = s.db.WithContext(ctx).Raw(`
		SELECT entity_id FROM catalog_external_ref
		 WHERE entity_type = ? AND source_id = ? AND external_id = ? AND link_kind = 0
		 ORDER BY id DESC LIMIT 1`,
		model.EntityTypeWork, sourceID, externalID).Scan(&workID).Error
	if err != nil {
		return 0, err
	}
	if workID == 0 {
		return 0, ErrPlaytimeRefUnresolved
	}
	return workID, nil
}

// ListMine pages a user's own reports in (updated_at, id) order for the sync
// leg. `since` is exclusive and may be zero for a first full pull.
//
// The order is the cursor: a client that stores the last updated_at it saw and
// sends it back gets exactly what changed, which is what makes a 300-game
// library cheap to keep in sync from two devices.
func (s *UserPlaytimeService) ListMine(ctx context.Context, uid int64, since time.Time, limit int) ([]PlaytimeRecord, error) {
	if uid <= 0 {
		return nil, ErrPlaytimeActorRequired
	}
	q := s.db.WithContext(ctx).Model(&model.CatalogUserPlaytime{}).Where("actor_uid = ?", uid)
	if !since.IsZero() {
		q = q.Where("updated_at > ?", since)
	}
	var rows []model.CatalogUserPlaytime
	if err := q.Order("updated_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]PlaytimeRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, PlaytimeRecord{
			WorkID: r.WorkID, Minutes: r.Minutes, Status: r.Status,
			LastPlayedAt: r.LastPlayedAt, ClientID: r.ClientID, UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// UserWorkPlaytime is a user's playtime on ONE work, folded across their
// clients — the shape the rating widget asks for.
type UserWorkPlaytime struct {
	WorkID       int64
	Minutes      int
	Status       int16
	LastPlayedAt *time.Time
	// Clients is how many of the user's apps reported here, so a UI can say
	// "from 2 apps" rather than implying one authoritative number.
	Clients int
}

// GetMine folds a user's reports on one work into a single answer: MAX minutes
// (two apps watching one save file are not two playthroughs — see the model),
// the strongest status any of them saw, and the latest sighting. Returns nil
// when the user has never reported on the work, which the handler renders as a
// 200 with a null body rather than a 404: "you have no playtime here" is an
// answer, not a missing resource.
func (s *UserPlaytimeService) GetMine(ctx context.Context, uid, workID int64) (*UserWorkPlaytime, error) {
	if uid <= 0 {
		return nil, ErrPlaytimeActorRequired
	}
	var rows []model.CatalogUserPlaytime
	if err := s.db.WithContext(ctx).
		Where("actor_uid = ? AND work_id = ?", uid, workID).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := UserWorkPlaytime{WorkID: workID, Clients: len(rows)}
	best := 0 // index of the row with the most minutes — its status is the default
	finished := false
	for i, r := range rows {
		if r.Minutes > rows[best].Minutes {
			best = i
		}
		if r.Status == model.PlaytimeStatusFinished {
			finished = true
		}
		if r.LastPlayedAt != nil && (out.LastPlayedAt == nil || r.LastPlayedAt.After(*out.LastPlayedAt)) {
			out.LastPlayedAt = r.LastPlayedAt
		}
	}
	out.Minutes = rows[best].Minutes
	out.Status = rows[best].Status
	// Finished outranks every other word: one app not having noticed the
	// credits roll does not un-finish the game.
	if finished {
		out.Status = model.PlaytimeStatusFinished
	}
	return &out, nil
}

// validateReport is the wire-value gate, split out so the handler's batch leg
// can reject one item without abandoning the other 199.
func validateReport(r PlaytimeReport) error {
	if r.ActorUID <= 0 {
		return ErrPlaytimeActorRequired
	}
	if r.ClientID == "" {
		return ErrPlaytimeClientRequired
	}
	if r.Minutes < 0 || r.Minutes > model.PlaytimeMinutesMax {
		return ErrPlaytimeMinutesRange
	}
	switch r.Status {
	case model.PlaytimeStatusPlaying, model.PlaytimeStatusFinished,
		model.PlaytimeStatusDropped, model.PlaytimeStatusOnHold:
	default:
		return ErrPlaytimeBadStatus
	}
	return nil
}

// assertReportableWork refuses a work that is not there to report on. Same
// three-way refusal as the vote face's guard, and for the same reason: a
// merged-away or retired id must not silently collect data nobody will read.
func assertReportableWork(tx *gorm.DB, workID int64) error {
	var work model.CatalogWork
	err := tx.Select("status").Where("id = ?", workID).Take(&work).Error
	switch {
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		return ErrPlaytimeWorkUnavailable
	case err != nil:
		return err
	case work.Status != model.WorkStatusLive:
		return ErrPlaytimeWorkUnavailable
	}
	return nil
}
