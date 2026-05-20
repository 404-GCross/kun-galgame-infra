package service

import (
	"context"
	"regexp"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"

	"gorm.io/gorm"
)

var vndbIDRegex = regexp.MustCompile(`^v\d+$`)

// ImageProbeFunc tests whether a batch of image_hashes still exist in
// image_service. Returns the subset that are missing (i.e. TTL-deleted
// or never uploaded). Used by Revert as a defence-in-depth pre-check
// against resurrecting a snapshot that points at a now-deleted image.
//
// nil ImageProbeFunc on the service means "skip probing" — Revert then
// applies the snapshot as-is (refping is supposed to keep all revision
// hashes alive, so the probe is a safety net, not a hard dependency).
type ImageProbeFunc func(ctx context.Context, hashes []string) (notFound []string, err error)

// GalgameService handles galgame business logic
type GalgameService struct {
	galgameRepo  *repository.GalgameRepository
	revisionRepo *repository.RevisionRepository
	prRepo       *repository.PRRepository
	userRepo     *repository.UserReadonlyRepository

	// probeImages is optional; when nil, Revert skips the dead-image
	// pre-check. Production wires this via WithImageProbe in cmd/galgame.
	probeImages ImageProbeFunc
}

// NewGalgameService creates a new GalgameService
func NewGalgameService(
	galgameRepo *repository.GalgameRepository,
	revisionRepo *repository.RevisionRepository,
	prRepo *repository.PRRepository,
	userRepo *repository.UserReadonlyRepository,
) *GalgameService {
	return &GalgameService{
		galgameRepo:  galgameRepo,
		revisionRepo: revisionRepo,
		prRepo:       prRepo,
		userRepo:     userRepo,
	}
}

// WithImageProbe wires the image_service existence probe used by Revert
// to detect snapshots that reference a TTL-deleted hash. Returns the
// service for fluent chaining: `NewGalgameService(...).WithImageProbe(fn)`.
// Passing nil is allowed and explicitly disables the probe (= behaviour
// without this call).
func (s *GalgameService) WithImageProbe(p ImageProbeFunc) *GalgameService {
	s.probeImages = p
	return s
}

// List returns a paginated list of galgames
func (s *GalgameService) List(ctx context.Context, req *dto.ListGalgameRequest) ([]model.Galgame, int64, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}
	return s.galgameRepo.List(ctx, req.Page, req.Limit, req.SortField, req.SortOrder, req.Search)
}

// GetByID returns a published galgame with relations (public visibility:
// status=0 only). Kept for callers without a viewer context.
func (s *GalgameService) GetByID(ctx context.Context, id int) (*model.Galgame, map[int]*dto.UserBrief, error) {
	return s.GetByIDWithViewer(ctx, id, 0)
}

// GetByIDWithViewer is the viewer-aware detail fetch. Visibility mirrors
// the repo's FindByIDsWithViewer / search predicate exactly:
//   - status=0                       → visible to anyone
//   - status ∈ {3,4}                 → visible only to the submitter
//   - status=1 (banned) / status=2 (VNDB draft) / other → NotFound
//
// status=2 stays hidden here on purpose: VNDB drafts are discovered via
// search and taken via POST /:gid/claim, never through this endpoint.
// Without this, an owner opening /edit/.../draft/<gid> for their own
// pending submission got "galgame 不存在" because the old code hard-cut
// every status != 0.
func (s *GalgameService) GetByIDWithViewer(ctx context.Context, id, viewerUID int) (*model.Galgame, map[int]*dto.UserBrief, error) {
	galgame, err := s.galgameRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, nil, err
	}

	switch {
	case galgame.Status == 0:
		// published — visible to all
	case (galgame.Status == 3 || galgame.Status == 4) && viewerUID > 0 && galgame.UserID == viewerUID:
		// submitter viewing their own pending / declined draft
	default:
		return nil, nil, errors.NewWithCode(errors.ErrGalgameNotFound)
	}

	// Batch user lookup
	userIDSet := map[int]bool{galgame.UserID: true}
	for _, c := range galgame.Contributor {
		userIDSet[c.UserID] = true
	}
	userIDs := make([]int, 0, len(userIDSet))
	for id := range userIDSet {
		userIDs = append(userIDs, id)
	}

	users, err := s.userRepo.FindByIDs(ctx, userIDs)
	if err != nil {
		users = make(map[int]*dto.UserBrief)
	}

	// Increment view asynchronously
	go func() {
		_ = s.galgameRepo.IncrementView(context.Background(), id)
	}()

	return galgame, users, nil
}

// Create creates a new galgame with revision 1
func (s *GalgameService) Create(ctx context.Context, uid int, req *dto.CreateGalgameRequest) (*model.Galgame, error) {
	if !vndbIDRegex.MatchString(req.VNDBID) {
		return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
	}

	exists, _, err := s.galgameRepo.ExistsByVNDBID(ctx, req.VNDBID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.NewWithCode(errors.ErrGalgameVNDBExists)
	}

	var newID int
	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Bare insert: only system fields. Status defaults 0 (published —
		// admin direct-create); vndb_id is set here so the partial-unique
		// index applies at insert. Every editable field is then written
		// by the SINGLE ApplySnapshot path — no manual relation loops, so
		// create can never drift from update/merge/revert again.
		g := model.Galgame{VNDBID: req.VNDBID, UserID: uid}
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		if err := repository.ApplySnapshot(tx, g.ID, uid, buildCreateSnapshot(req)); err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameContributor{GalgameID: g.ID, UserID: uid}).Error; err != nil {
			return err
		}

		full, err := loadGalgameWithRelations(tx, g.ID)
		if err != nil {
			return err
		}
		snapshotJSON, err := model.TakeSnapshot(full).ToJSON()
		if err != nil {
			return err
		}
		newID = g.ID
		return tx.Create(&model.GalgameRevision{
			GalgameID: g.ID,
			Revision:  1,
			UserID:    uid,
			Action:    "created",
			Snapshot:  snapshotJSON,
		}).Error
	})

	if err != nil {
		return nil, err
	}

	return s.galgameRepo.FindByID(ctx, newID)
}

// buildCreateSnapshot assembles the revision-1 snapshot for a brand-new
// galgame (admin direct-create). Defaults mirror the galgame column
// defaults; the VNDB link is materialised into Links so ApplySnapshot is
// the only writer. `bid` is intentionally absent (reserved, see §1.5).
func buildCreateSnapshot(req *dto.CreateGalgameRequest) *model.Snapshot {
	s := &model.Snapshot{
		VNDBID:           req.VNDBID,
		ReleaseDate:      strNonEmpty(req.ReleaseDate),
		ReleaseDateTBA:   req.ReleaseDateTBA,
		NameEnUS:         req.NameEnUS,
		NameJaJP:         req.NameJaJP,
		NameZhCN:         req.NameZhCN,
		NameZhTW:         req.NameZhTW,
		Banner:           req.Banner,
		BannerImageHash:  req.BannerImageHash,
		IntroEnUS:        req.IntroEnUS,
		IntroJaJP:        req.IntroJaJP,
		IntroZhCN:        req.IntroZhCN,
		IntroZhTW:        req.IntroZhTW,
		ContentLimit:     orDefault(req.ContentLimit, "sfw"),
		OriginalLanguage: orDefault(req.OriginalLanguage, "ja-jp"),
		AgeLimit:         orDefault(req.AgeLimit, "r18"),
		SeriesID:         req.SeriesID,
		Aliases:          splitCSV(req.Aliases),
		TagIDs:           req.TagIDs,
		OfficialIDs:      req.OfficialIDs,
		EngineIDs:        req.EngineIDs,
		Links:            vndbLink(req.VNDBID),
		Covers:           coverInputsToSnapshot(req.Covers),
		Screenshots:      screenshotInputsToSnapshot(req.Screenshots),
	}
	return s
}

// coverInputsToSnapshot lifts the dto-layer slice into the model
// SnapshotCover form (drops the GalgameID — that's set by ApplySnapshot).
// Lives in the service package so dto stays model-free.
func coverInputsToSnapshot(in []dto.GalgameCoverInput) []model.SnapshotCover {
	out := make([]model.SnapshotCover, 0, len(in))
	for _, c := range in {
		out = append(out, model.SnapshotCover{
			ImageHash: c.ImageHash, SortOrder: c.SortOrder,
			Sexual: c.Sexual, Violence: c.Violence,
			Source: c.Source, SourceKey: c.SourceKey,
		})
	}
	return out
}

func screenshotInputsToSnapshot(in []dto.GalgameScreenshotInput) []model.SnapshotScreenshot {
	out := make([]model.SnapshotScreenshot, 0, len(in))
	for _, sh := range in {
		out = append(out, model.SnapshotScreenshot{
			ImageHash: sh.ImageHash, SortOrder: sh.SortOrder,
			Caption: sh.Caption,
			Sexual:  sh.Sexual, Violence: sh.Violence,
			Source: sh.Source, SourceKey: sh.SourceKey,
		})
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// strNonEmpty returns &s when s is non-empty, else nil. Used to flatten an
// optional date string (DTO uses "" = unknown) into the snapshot's *string
// shape (nil = unknown). The two representations stay distinct so DTO
// "no value" cannot accidentally collide with a real empty input.
func strNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func splitCSV(csv string) []string {
	out := make([]string, 0)
	for _, p := range strings.Split(csv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// vndbLink returns the single auto VNDB link for a galgame, or empty.
func vndbLink(vndbID string) []model.SnapshotLink {
	if vndbID == "" {
		return []model.SnapshotLink{}
	}
	return []model.SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/" + vndbID}}
}

// Update directly updates a galgame (creator or admin) and creates a new revision
func (s *GalgameService) Update(ctx context.Context, uid, galgameID int, roles []string, req *dto.UpdateGalgameRequest) (*model.Galgame, error) {
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, err
	}

	if galgame.UserID != uid && !hasRole(roles, "admin") {
		return nil, errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	// Direct edit = "merge a snapshot against yourself": overlay the
	// request onto the current canonical snapshot, and if nothing
	// actually changed (incl. relations), no-op without a revision.
	full, err := loadGalgameWithRelations(s.galgameRepo.DB().WithContext(ctx), galgameID)
	if err != nil {
		return nil, err
	}
	cur := model.TakeSnapshot(full)
	next := overlayUpdate(cur, req)
	if len(model.ChangedKeys(cur, next)) == 0 {
		return galgame, nil
	}

	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Single canonical write path — same one revert / PR-merge use.
		// Scalar fields updated + every relation table cleared+rebuilt
		// from `next`, so tag/official/engine edits actually persist.
		if err := repository.ApplySnapshot(tx, galgameID, uid, next); err != nil {
			return err
		}

		nextRev, err := repository.NextRevision(tx, galgameID)
		if err != nil {
			return err
		}

		// Snapshot is re-taken from the just-written state so it is
		// canonical and provably == DB == the edit's intent (the old
		// code recorded a snapshot taken from un-mutated relations,
		// corrupting history — that is fixed here).
		fullAfter, err := loadGalgameWithRelations(tx, galgameID)
		if err != nil {
			return err
		}
		snapshotJSON, err := model.TakeSnapshot(fullAfter).ToJSON()
		if err != nil {
			return err
		}

		var count int64
		tx.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = ?", galgameID, uid).Count(&count)
		if count == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: galgameID, UserID: uid}).Error; err != nil {
				return err
			}
		}

		isMinor := false
		if req.IsMinor != nil {
			isMinor = *req.IsMinor
		}

		return tx.Create(&model.GalgameRevision{
			GalgameID: galgameID,
			Revision:  nextRev,
			UserID:    uid,
			Action:    "updated",
			Snapshot:  snapshotJSON,
			IsMinor:   isMinor,
		}).Error
	})

	if err != nil {
		return nil, err
	}

	return s.galgameRepo.FindByID(ctx, galgameID)
}

// BatchGet returns lightweight galgame info for a list of IDs (status=0 only).
func (s *GalgameService) BatchGet(ctx context.Context, ids []int) ([]dto.GalgameBrief, error) {
	return s.BatchGetWithViewer(ctx, ids, 0)
}

// BatchGetWithViewer returns lightweight galgame info for a list of IDs.
// When viewerUID > 0, additionally includes the viewer's own status=3/4
// entries (per submission-and-review-design §6).
func (s *GalgameService) BatchGetWithViewer(ctx context.Context, ids []int, viewerUID int) ([]dto.GalgameBrief, error) {
	galgames, err := s.galgameRepo.FindByIDsWithViewer(ctx, ids, viewerUID)
	if err != nil {
		return nil, err
	}

	// Single batch lookup for the pinned cover of every brief — avoids
	// O(N) per-row queries while still letting EffectiveBannerHash come
	// out non-nil on the response.
	resultIDs := make([]int, 0, len(galgames))
	for _, g := range galgames {
		resultIDs = append(resultIDs, g.ID)
	}
	pinned, err := s.galgameRepo.PinnedCoverHashes(ctx, resultIDs)
	if err != nil {
		// Non-fatal: fall back to BannerImageHash on each row below.
		pinned = map[int]string{}
	}

	items := make([]dto.GalgameBrief, len(galgames))
	for i, g := range galgames {
		var effective *string
		if h, ok := pinned[g.ID]; ok {
			effective = &h
		} else if g.BannerImageHash != nil && *g.BannerImageHash != "" {
			// Migration-window fallback: row not yet backfilled to cover table.
			v := *g.BannerImageHash
			effective = &v
		}
		items[i] = dto.GalgameBrief{
			ID:                  g.ID,
			VNDBID:              g.VNDBID,
			NameEnUS:            g.NameEnUS,
			NameJaJP:            g.NameJaJP,
			NameZhCN:            g.NameZhCN,
			NameZhTW:            g.NameZhTW,
			Banner:              g.Banner,
			BannerImageHash:     g.BannerImageHash,
			EffectiveBannerHash: effective,
			ContentLimit:        g.ContentLimit,
			Status:              g.Status,
			UserID:              g.UserID,
			ResourceUpdateTime:  g.ResourceUpdateTime.Format("2006-01-02T15:04:05Z"),
			OriginalLanguage:    g.OriginalLanguage,
			AgeLimit:            g.AgeLimit,
		}
	}
	return items, nil
}

// CheckVNDB checks if a VNDB ID already exists
func (s *GalgameService) CheckVNDB(ctx context.Context, vndbID string) (bool, int, error) {
	return s.galgameRepo.ExistsByVNDBID(ctx, vndbID)
}

// GetUserStats returns aggregated galgame statistics for a user
func (s *GalgameService) GetUserStats(ctx context.Context, uid int) (*dto.UserGalgameStats, error) {
	return s.galgameRepo.GetUserStats(ctx, uid)
}

// loadGalgameWithRelations loads a galgame with all relations using the given tx.
// Cover/Screenshot get an explicit ORDER BY so consumers (and effective
// banner derivation) see a deterministic sequence — sort_order asc,
// then created asc as a stable tiebreak for the non-pinned tail.
func loadGalgameWithRelations(tx *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := tx.
		Preload("Alias").
		Preload("Tag").
		Preload("Official").
		Preload("Engine").
		Preload("Link").
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Preload("Screenshot", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		First(&g, id).Error
	if err == nil {
		model.PopulateEffectiveBanner(&g)
	}
	return &g, err
}

// overlayUpdate builds the *target snapshot* of a direct edit: it starts
// from the current canonical snapshot and overlays only the fields the
// request actually carries (pointer-presence semantics — nil leaves a
// field unchanged, a set value incl. empty string / empty slice is an
// authoritative replacement). This is the single place "what does this
// edit change" is expressed; repository.ApplySnapshot then writes it and
// the revision records exactly it. Every editable model.Snapshot field
// is reachable here (released / aliases / links / tag_ids / official_ids
// / engine_ids included); the only reserved exception is `bid`
// (BangumiID) — sync-managed, intentionally not user-editable, carried
// over from `cur` untouched so revert keeps any synced value. See the
// invariant in docs/galgame_wiki/01-revision-system-design.md §1.5.
func overlayUpdate(cur *model.Snapshot, req *dto.UpdateGalgameRequest) *model.Snapshot {
	n := *cur // copy; slices are replaced wholesale below, never mutated in place
	if req.VNDBID != nil {
		n.VNDBID = *req.VNDBID
	}
	if req.NameEnUS != nil {
		n.NameEnUS = *req.NameEnUS
	}
	if req.NameJaJP != nil {
		n.NameJaJP = *req.NameJaJP
	}
	if req.NameZhCN != nil {
		n.NameZhCN = *req.NameZhCN
	}
	if req.NameZhTW != nil {
		n.NameZhTW = *req.NameZhTW
	}
	if req.Banner != nil {
		n.Banner = *req.Banner
	}
	if req.BannerImageHash != nil {
		// "" clears it (ApplySnapshot maps empty → NULL column).
		n.BannerImageHash = *req.BannerImageHash
	}
	if req.IntroEnUS != nil {
		n.IntroEnUS = *req.IntroEnUS
	}
	if req.IntroJaJP != nil {
		n.IntroJaJP = *req.IntroJaJP
	}
	if req.IntroZhCN != nil {
		n.IntroZhCN = *req.IntroZhCN
	}
	if req.IntroZhTW != nil {
		n.IntroZhTW = *req.IntroZhTW
	}
	if req.ContentLimit != nil {
		n.ContentLimit = *req.ContentLimit
	}
	if req.OriginalLanguage != nil {
		n.OriginalLanguage = *req.OriginalLanguage
	}
	if req.AgeLimit != nil {
		n.AgeLimit = *req.AgeLimit
	}
	if req.ReleaseDate != nil {
		// presence: nil = keep; non-nil & "" = clear to unknown; non-nil & date = set.
		// validate.datetime upstream guarantees non-empty values are well-formed.
		if *req.ReleaseDate == "" {
			n.ReleaseDate = nil
		} else {
			v := *req.ReleaseDate
			n.ReleaseDate = &v
		}
	}
	if req.ReleaseDateTBA != nil {
		n.ReleaseDateTBA = *req.ReleaseDateTBA
	}
	if req.SeriesID != nil {
		v := *req.SeriesID
		n.SeriesID = &v
	}
	if req.Aliases != nil {
		n.Aliases = append([]string(nil), (*req.Aliases)...)
	}
	if req.Links != nil {
		links := make([]model.SnapshotLink, 0, len(*req.Links))
		for _, l := range *req.Links {
			links = append(links, model.SnapshotLink{Name: l.Name, Link: l.Link})
		}
		n.Links = links
	}
	if req.TagIDs != nil {
		n.TagIDs = append([]int(nil), (*req.TagIDs)...)
	}
	if req.OfficialIDs != nil {
		n.OfficialIDs = append([]int(nil), (*req.OfficialIDs)...)
	}
	if req.EngineIDs != nil {
		n.EngineIDs = append([]int(nil), (*req.EngineIDs)...)
	}
	if req.Covers != nil {
		n.Covers = coverInputsToSnapshot(*req.Covers)
	}
	if req.Screenshots != nil {
		n.Screenshots = screenshotInputsToSnapshot(*req.Screenshots)
	}
	return &n
}

// CreateRevisionFromCurrentState takes a snapshot of the current galgame state
// and creates a new revision. Must be called inside a transaction.
func (s *GalgameService) CreateRevisionFromCurrentState(tx *gorm.DB, galgameID, userID int, action, note string, isMinor bool) error {
	fullGalgame, err := loadGalgameWithRelations(tx, galgameID)
	if err != nil {
		return err
	}
	snapshot := model.TakeSnapshot(fullGalgame)
	snapshotJSON, err := snapshot.ToJSON()
	if err != nil {
		return err
	}

	nextRev, err := repository.NextRevision(tx, galgameID)
	if err != nil {
		return err
	}

	return tx.Create(&model.GalgameRevision{
		GalgameID: galgameID,
		Revision:  nextRev,
		UserID:    userID,
		Action:    action,
		Note:      note,
		Snapshot:  snapshotJSON,
		IsMinor:   isMinor,
	}).Error
}

// optStr returns *p if non-nil, else "". Used to flatten optional string
// pointer fields into the model layer where empty-string-as-null works.
func optStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// strToPtr converts an empty string to nil; otherwise returns a pointer
// to the string. Useful for nullable VARCHAR columns where "" means
// "no value" rather than empty-string-stored.
func strToPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
