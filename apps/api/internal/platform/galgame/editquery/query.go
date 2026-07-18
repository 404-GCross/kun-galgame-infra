// Package editquery holds the galgame family's native read queries over the
// unified editing-engine log (edit_revision / edit_proposal on the catalog
// pool, charter ruling 2). It is the graduated successor to the retired
// editbridge old-wire ADAPTER: after E3b the galgame surface no longer
// reconstructs the frozen galgame_revision / galgame_pr wire shapes, but two
// native reads still ride the engine tables —
//
//   - Feed: the merged-revision S2S cron feed (GET /galgame/revisions/recent),
//     cursored on the WIRE id = COALESCE(legacy_id, id) so downstream
//     kungal/moyu since_id cursors saved against the old table survive the E2b
//     migration untouched;
//   - UserEditStats: a user's revision/PR contribution counters — the source
//     for the profile stats and the forum creator-eligibility gate.
//
// Both read only engine columns (no snapshot re-keying, no wire-shape
// reconstruction), so the graduation is byte-clean: the feed line contract and
// cursor bytes are identical to the editbridge era. The engine's Go models stay
// pure — the legacy_* bookkeeping columns are mapped here by package-local row
// structs (the same posture the retired editbridge used).
package editquery

import (
	"context"
	"time"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"

	"gorm.io/gorm"
)

// Querier runs the native galgame edit-log reads over the engine pool.
type Querier struct {
	db *gorm.DB // engine pool (catalog DB, charter ruling 2)
}

// New builds a Querier over the engine pool (deps.EditDB at the assembly point).
func New(db *gorm.DB) *Querier { return &Querier{db: db} }

// revisionRow maps the edit_revision columns the reads need, including the
// legacy bookkeeping columns. Every column tag is explicit (the UID→u_i_d
// acronym snake-case trap).
type revisionRow struct {
	ID           int64     `gorm:"column:id"`
	EntityID     int64     `gorm:"column:entity_id"`
	Seq          int       `gorm:"column:seq"`
	Action       int16     `gorm:"column:action"`
	ActorUID     int64     `gorm:"column:actor_uid"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	LegacyAction *string   `gorm:"column:legacy_action"`
	LegacyID     *int64    `gorm:"column:legacy_id"`
}

func (revisionRow) TableName() string { return "edit_revision" }

// wireID is the old-wire id: COALESCE(legacy_id, id) — the source-row PK for
// migrated rows, the engine id for new-era rows (the transform bumped the
// identity sequence past every legacy id, so the two ranges never collide).
func (r *revisionRow) wireID() int64 {
	if r.LegacyID != nil {
		return *r.LegacyID
	}
	return r.ID
}

// proposalRow maps the edit_proposal columns UserEditStats counts.
type proposalRow struct {
	ProposerUID int64 `gorm:"column:proposer_uid"`
	Status      int16 `gorm:"column:status"`
}

func (proposalRow) TableName() string { return "edit_proposal" }

func (q *Querier) revisions(ctx context.Context) *gorm.DB {
	return q.db.WithContext(ctx).Model(&revisionRow{}).
		Where("entity_family = ? AND entity_type = ?", "galgame", editspec.TypeGame)
}

func (q *Querier) proposals(ctx context.Context) *gorm.DB {
	return q.db.WithContext(ctx).Model(&proposalRow{}).
		Where("entity_family = ? AND entity_type = ?", "galgame", editspec.TypeGame)
}

// FeedItem is one merged-revision event on the /galgame/revisions/recent wire.
type FeedItem struct {
	ID        int64 // wire id = COALESCE(legacy_id, id)
	GalgameID int
	Revision  int
	UserID    int
	Action    string
	Created   time.Time
}

// Feed returns the merged-revision feed ("an edit landed" events: old action
// 'merged', or new-era engine action merged = a reviewer merged a proposal),
// cursored and ordered by the WIRE id so downstream since_id cursors survive
// the migration. Snapshot is never selected (feed rows are narrow on purpose).
// Byte-identical to the retired editbridge.Feed — same filter, cursor, order,
// and column set.
func (q *Querier) Feed(ctx context.Context, sinceID int64, limit int) ([]FeedItem, error) {
	query := q.revisions(ctx).
		Where("(legacy_action = 'merged' OR (legacy_action IS NULL AND action = ?))", editing.ActionMerged)
	if sinceID > 0 {
		query = query.Where("COALESCE(legacy_id, id) > ?", sinceID)
	}
	var rows []revisionRow
	if err := query.Select("id", "entity_id", "seq", "action", "actor_uid", "legacy_action", "legacy_id", "created_at").
		Order("COALESCE(legacy_id, id) ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]FeedItem, len(rows))
	for i := range rows {
		r := &rows[i]
		out[i] = FeedItem{
			ID:        r.wireID(),
			GalgameID: int(r.EntityID),
			Revision:  r.Seq,
			UserID:    int(r.ActorUID),
			Action:    actionString(r),
			Created:   r.CreatedAt,
		}
	}
	return out, nil
}

// actionString renders the old-wire action label the feed has always emitted:
// honest provenance for migrated rows (legacy_action), the engine vocabulary
// mapped back for new-era rows (direct edits show as the old "updated").
func actionString(r *revisionRow) string {
	if r.LegacyAction != nil {
		return *r.LegacyAction
	}
	switch r.Action {
	case editing.ActionCreated:
		return "created"
	case editing.ActionMerged:
		return "merged"
	case editing.ActionReverted:
		return "reverted"
	default: // editing.ActionDirect
		return "updated"
	}
}

// UserEditStats aggregates one user's galgame edit activity from the engine
// tables (galgame_revision / galgame_pr are frozen). Historical attribution
// survives the migration (actor_uid / proposer_uid are carried over); the
// pending/declined PRs that D2 discarded are honestly absent from the counts.
type UserEditStats struct {
	RevisionCount int
	PRSubmitted   int
	PRMerged      int
	PRDeclined    int
	PRPending     int
}

// UserEditStats counts revisions authored by uid and proposals filed by uid
// (galgame.game rows only). Withdrawn proposals count as declined (the old
// status vocabulary the profile page renders has no withdrawn state).
func (q *Querier) UserEditStats(ctx context.Context, uid int) (*UserEditStats, error) {
	out := &UserEditStats{}
	count := func(query *gorm.DB) (int, error) {
		var n int64
		if err := query.Count(&n).Error; err != nil {
			return 0, err
		}
		return int(n), nil
	}
	var err error
	if out.RevisionCount, err = count(q.revisions(ctx).Where("actor_uid = ?", uid)); err != nil {
		return nil, err
	}
	if out.PRSubmitted, err = count(q.proposals(ctx).Where("proposer_uid = ?", uid)); err != nil {
		return nil, err
	}
	if out.PRMerged, err = count(q.proposals(ctx).Where("proposer_uid = ? AND status = ?", uid, editing.StatusMerged)); err != nil {
		return nil, err
	}
	if out.PRDeclined, err = count(q.proposals(ctx).Where("proposer_uid = ? AND status IN ?", uid,
		[]int16{editing.StatusDeclined, editing.StatusWithdrawn})); err != nil {
		return nil, err
	}
	if out.PRPending, err = count(q.proposals(ctx).Where("proposer_uid = ? AND status = ?", uid, editing.StatusOpen)); err != nil {
		return nil, err
	}
	return out, nil
}
