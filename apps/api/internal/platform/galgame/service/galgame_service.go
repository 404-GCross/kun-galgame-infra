package service

import (
	"context"
	"regexp"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"
	"api/pkg/utils"

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

// List returns a paginated list of galgames.
//
// content_limit policy: browse endpoint → safe-by-default "sfw" when
// caller omits the parameter. Pass `?content_limit=all` to include NSFW,
// `?content_limit=nsfw` to fetch NSFW-only.
//
// released_from / released_to: accept YYYY or YYYY-MM. Empty = no filter.
// Invalid input returns a 400 (parse error surfaces as ErrValidationFailed).
func (s *GalgameService) List(ctx context.Context, req *dto.ListGalgameRequest) ([]model.Galgame, int64, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 24
	}
	// Honor the DTO's documented max=50 (the validate tag is dead — no
	// app-wide StructValidator). Without this, ?limit=1000000 runs a single
	// unbounded heavily-preloaded query on a public, unauthenticated route.
	if req.Limit > 50 {
		req.Limit = 50
	}
	contentLimit := utils.ParseContentLimit(req.ContentLimit, "sfw")

	from, err := utils.ParseReleaseLowerBound(req.ReleasedFrom)
	if err != nil {
		return nil, 0, errors.New(errors.ErrValidationFailed, err.Error())
	}
	to, err := utils.ParseReleaseUpperBound(req.ReleasedTo)
	if err != nil {
		return nil, 0, errors.New(errors.ErrValidationFailed, err.Error())
	}
	months, err := utils.ParseMonthSet(req.ReleasedMonths)
	if err != nil {
		return nil, 0, errors.New(errors.ErrValidationFailed, err.Error())
	}

	return s.galgameRepo.List(ctx, req.Page, req.Limit, req.SortField, req.SortOrder, req.Search, contentLimit, from, to, months)
}

// GetByID returns a published galgame with relations (public visibility:
// status=0 only). Kept for callers without a viewer context.
//
// No content_limit filter — equivalent to passing "" (any) to
// GetByIDWithViewer. Matches the long-standing semantic where a known
// gid resolves regardless of NSFW state. Callers that need filtering
// should use GetByIDWithViewer directly with a non-empty contentLimit.
func (s *GalgameService) GetByID(ctx context.Context, id int) (*model.Galgame, map[int]*dto.UserBrief, error) {
	return s.GetByIDWithViewer(ctx, id, 0, "")
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
func (s *GalgameService) GetByIDWithViewer(ctx context.Context, id, viewerUserID int, contentLimit string) (*model.Galgame, map[int]*dto.UserBrief, error) {
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
	case (galgame.Status == 3 || galgame.Status == 4) && viewerUserID > 0 && galgame.UserID == viewerUserID:
		// submitter viewing their own pending / declined draft
	default:
		return nil, nil, errors.NewWithCode(errors.ErrGalgameNotFound)
	}

	// content_limit filter: when caller passed sfw/nsfw, hide entries
	// that don't match (404, same shape as status filter above). "" = no
	// filter, matches the long-standing behavior of direct-id lookup.
	//
	// Applied AFTER the status gate so that an authenticated submitter's
	// own NSFW pending draft is still gated by their own filter request
	// — consistent with how the search "pending" list inherits the same
	// content_limit (search/service.go second-pass query).
	if contentLimit != "" && galgame.ContentLimit != contentLimit {
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

	// Increment view asynchronously — only for the public, published view.
	// A submitter opening their own pending(3)/declined(4) draft must not
	// inflate the persisted `view` count (it is JSON-exposed and a whitelisted
	// public sort field that carries over on publish).
	if galgame.Status == model.GalgameStatusPublished {
		go func() {
			_ = s.galgameRepo.IncrementView(context.Background(), id)
		}()
	}

	return galgame, users, nil
}

// Create creates a new galgame with revision 1
func (s *GalgameService) Create(ctx context.Context, userID int, req *dto.CreateGalgameRequest) (*model.Galgame, error) {
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
		g := model.Galgame{VNDBID: req.VNDBID, UserID: userID}
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		if err := repository.ApplySnapshot(tx, g.ID, userID, buildCreateSnapshot(req)); err != nil {
			return err
		}
		if err := tx.Create(&model.GalgameContributor{GalgameID: g.ID, UserID: userID}).Error; err != nil {
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
			UserID:    userID,
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
	// Multipart-uploaded banner becomes the pinned cover. Merge AFTER
	// the explicit Covers field so the upload wins the sort_order=0 slot
	// even when the JSON body specified its own cover set.
	if req.PromoteCoverHash != "" {
		s.Covers = promoteCoverHashInPlace(s.Covers, req.PromoteCoverHash)
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

// promoteCoverHashInPlace mutates covers so that the given hash sits at
// sort_order=0. If covers already contains a row with that hash, that
// row is moved to sort_order=0 and any prior sort_order=0 entry is
// demoted to sort_order=1. If covers does NOT yet contain the hash, a
// fresh entry is inserted at sort_order=0 (defaults for the metadata
// columns; admin can edit them after the fact).
//
// The pinned-cover partial unique index requires that at most one row
// in the final state has sort_order=0, hence the demote step. Other
// rows' SortOrder values are left untouched — caller decides whether to
// renumber them.
//
// Used after a multipart-uploaded banner (handler sets the transient
// PromoteCoverHash field on the request, service routes it here before
// ApplySnapshot writes the snapshot).
func promoteCoverHashInPlace(covers []model.SnapshotCover, hash string) []model.SnapshotCover {
	if hash == "" {
		return covers
	}
	promoteIdx := -1
	for i, c := range covers {
		if c.ImageHash == hash {
			promoteIdx = i
			break
		}
	}
	// Demote any existing pinned entry (partial-unique constraint).
	for i := range covers {
		if covers[i].SortOrder == 0 && (promoteIdx < 0 || i != promoteIdx) {
			covers[i].SortOrder = 1
		}
	}
	if promoteIdx >= 0 {
		covers[promoteIdx].SortOrder = 0
		return covers
	}
	// Hash is new; insert at the front so JSON readers see it first.
	return append([]model.SnapshotCover{{ImageHash: hash, SortOrder: 0}}, covers...)
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
func (s *GalgameService) Update(ctx context.Context, userID, galgameID int, roles []string, req *dto.UpdateGalgameRequest) (*model.Galgame, error) {
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, err
	}

	if galgame.UserID != userID && !hasRole(roles, "admin") {
		return nil, errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	// PUT is "direct edit of a PUBLISHED entry" (docs 06/07). A submitter's
	// own pending(3)/declined(4) draft must go through PATCH/PatchDraft, which
	// flips declined→pending and emits the review-queue message. Admins keep
	// direct-edit on any status (e.g. fixing a VNDB draft).
	if galgame.Status != model.GalgameStatusPublished && !hasRole(roles, "admin") {
		return nil, errors.NewWithCode(errors.ErrGalgameDraftStatusInvalid)
	}

	// Validate a vndb_id change the same way Create/Submit do — overlayUpdate
	// would otherwise persist a malformed id, and a duplicate would surface as
	// a raw 500 (unique-index violation) instead of the actionable 20004.
	if req.VNDBID != nil && *req.VNDBID != galgame.VNDBID {
		if v := *req.VNDBID; v != "" {
			if !vndbIDRegex.MatchString(v) {
				return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
			}
			exists, existingID, err := s.galgameRepo.ExistsByVNDBID(ctx, v)
			if err != nil {
				return nil, err
			}
			if exists && existingID != galgameID {
				return nil, errors.NewWithCode(errors.ErrGalgameVNDBExists)
			}
		}
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
		if err := repository.ApplySnapshot(tx, galgameID, userID, next); err != nil {
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
		tx.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = ?", galgameID, userID).Count(&count)
		if count == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: galgameID, UserID: userID}).Error; err != nil {
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
			UserID:    userID,
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
//
// Unfiltered by content_limit — preserves the long-standing batch
// semantic where callers already know the IDs they want. For SFW
// filtering pass an explicit value through BatchGetWithViewer.
func (s *GalgameService) BatchGet(ctx context.Context, ids []int) ([]dto.GalgameBrief, error) {
	return s.BatchGetWithViewer(ctx, ids, 0, "")
}

// BatchGetWithViewer returns lightweight galgame info for a list of IDs.
// When viewerUserID > 0, additionally includes the viewer's own status=3/4
// entries (per submission-and-review-design §6).
//
// contentLimit follows utils.ParseContentLimit semantics — pass "" for no
// filter (the canonical batch default; caller knows the IDs they want),
// or "sfw"/"nsfw" to filter. /galgame/batch handler resolves an empty
// query param to "" (not "sfw"), so silent hiding of explicitly-requested
// IDs never happens.
func (s *GalgameService) BatchGetWithViewer(ctx context.Context, ids []int, viewerUserID int, contentLimit string) ([]dto.GalgameBrief, error) {
	galgames, err := s.galgameRepo.FindByIDsWithViewer(ctx, ids, viewerUserID, contentLimit)
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
		// Non-fatal: covers query failed → effective_banner_hash stays nil
		// for every row this batch. Frontend renders the legacy Banner
		// URL fallback in resolveBannerUrl when no hash is available.
		pinned = map[int]string{}
	}

	items := make([]dto.GalgameBrief, len(galgames))
	for i, g := range galgames {
		var effective *string
		if h, ok := pinned[g.ID]; ok {
			effective = &h
		}
		items[i] = dto.GalgameBrief{
			ID:                  g.ID,
			VNDBID:              g.VNDBID,
			NameEnUS:            g.NameEnUS,
			NameJaJP:            g.NameJaJP,
			NameZhCN:            g.NameZhCN,
			NameZhTW:            g.NameZhTW,
			Banner:              g.Banner,
			EffectiveBannerHash: effective,
			ContentLimit:        g.ContentLimit,
			Status:              g.Status,
			UserID:              g.UserID,
			ResourceUpdateTime:  g.ResourceUpdateTime.Time().UTC().Format("2006-01-02T15:04:05Z"),
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
func (s *GalgameService) GetUserStats(ctx context.Context, userID int) (*dto.UserGalgameStats, error) {
	return s.galgameRepo.GetUserStats(ctx, userID)
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
	// Multipart-uploaded banner promotes itself into the cover set. Run
	// AFTER the Covers overlay above so an explicit JSON Covers field
	// can still control non-pinned slots while the uploaded image takes
	// sort_order=0. When req.Covers is nil, this promotes within the
	// preserved current covers (n.Covers came from cur via the copy).
	if req.PromoteCoverHash != "" {
		// Deep-copy first so we don't mutate the cur snapshot the caller
		// might still be holding (it's used for ChangedKeys upstream).
		n.Covers = append([]model.SnapshotCover(nil), n.Covers...)
		n.Covers = promoteCoverHashInPlace(n.Covers, req.PromoteCoverHash)
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
