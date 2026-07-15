package service

import (
	"context"
	"regexp"
	"strings"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
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

	// imageMeta fetches intrinsic image metadata (width/height/thumbhash) from
	// image_service to enrich covers / screenshots / banners at READ time; nil
	// disables enrichment (images still render, just without dimensions or a
	// blur-up placeholder). Wired via WithImageMeta in cmd/galgame. metaCache
	// memoizes results — safe forever because metadata is immutable per
	// content-addressed hash. See image_meta.go.
	imageMeta ImageMetaFunc
	metaCache *imageMetaCache

	// cdnBase is the image-service CDN URL prefix used by the open-API public
	// projection to emit complete image URLs instead of bare hashes. Empty
	// disables URL composition (the public face then omits images). Wired via
	// WithCDNBase in cmd/galgame; irrelevant to every internal read path (they
	// return the bare hash and let the frontend resolve the URL).
	cdnBase string
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
		metaCache:    newImageMetaCache(200000),
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

// WithCDNBase wires the image-service CDN base URL used by the open-API public
// projection to compose complete image URLs. Returns the service for fluent
// chaining. Empty base = the public face omits images (bare hashes are never
// leaked on the public wire).
func (s *GalgameService) WithCDNBase(base string) *GalgameService {
	s.cdnBase = base
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
	return s.GetByIDWithViewer(ctx, id, 0, nil, "")
}

// GetByIDWithViewer is the viewer-aware detail fetch. Visibility:
//   - status=0                       → visible to anyone
//   - status ∈ {3,4}                 → visible to the submitter, AND to a
//     reviewer (holder of galgame.review: moderator/admin/ren)
//     so the review queue's "查看" can
//     preview a pending/declined submission
//   - status=1 (banned) / status=2 (VNDB draft) / other → NotFound
//
// status=2 stays hidden even for reviewers on purpose: VNDB drafts are
// discovered via search and taken via POST /:gid/claim, never through this
// endpoint. The submitter case is what lets an owner open their own
// /edit/.../draft/<gid>; the reviewer case is what lets the kungal/moyu
// review queue open someone else's pending(3) submission (a reviewer who
// is not the submitter otherwise hit "galgame 不存在").
func (s *GalgameService) GetByIDWithViewer(ctx context.Context, id, viewerUserID int, viewerRoles []string, contentLimit string) (*model.Galgame, map[int]*dto.UserBrief, error) {
	galgame, err := s.galgameRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, nil, err
	}

	// reviewer = holds galgame.review (moderator/admin/ren) — the review-queue
	// staff, the same capability that gates /admin/galgame/* and any-status
	// edits (Update below).
	reviewer := perm.Resolver.Can(viewerRoles, perm.Review)

	switch {
	case galgame.Status == 0:
		// published — visible to all
	case (galgame.Status == 3 || galgame.Status == 4) && viewerUserID > 0 && galgame.UserID == viewerUserID:
		// submitter viewing their own pending / declined draft
	case (galgame.Status == 3 || galgame.Status == 4) && reviewer:
		// admin / moderator previewing any pending / declined submission
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
	//
	// Bypassed when a reviewer is previewing a non-published submission: a
	// reviewer must be able to open an NSFW pending(3)/declined(4) draft for
	// review regardless of their own sfw/nsfw filter, otherwise the review
	// "查看" would 404 on exactly the submissions that most need a look.
	// Published (status=0) browsing still filters normally, even for admins.
	reviewingDraft := reviewer && galgame.Status != 0
	if contentLimit != "" && galgame.ContentLimit != contentLimit && !reviewingDraft {
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

	// Fill covers/screenshots with image_service dimensions + thumbhash so the
	// detail page reserves the right aspect ratio and shows a blur-up
	// placeholder (no crop, no layout shift). Non-fatal / cached; no-op when
	// enrichment is unwired.
	s.enrichGalgameImages(ctx, galgame)

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
		snap := model.TakeSnapshot(full)
		snapshotJSON, err := snap.ToJSON()
		if err != nil {
			return err
		}
		newID = g.ID
		rev := &model.GalgameRevision{
			GalgameID: g.ID,
			Revision:  1,
			UserID:    userID,
			Action:    "created",
			Snapshot:  snapshotJSON,
		}
		// created → every populated field is "new" (delta vs empty).
		rev.SetChangedFields(model.KeysOf(model.ChangedKeys(&model.Snapshot{}, snap)))
		return tx.Create(rev).Error
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
		ReleaseDate:      model.NormalizeReleaseDateInput(req.ReleaseDate),
		ReleaseDateTBA:   req.ReleaseDateTBA,
		ReleasePrecision: string(model.DeriveInputPrecision(req.ReleaseDate, req.ReleaseDateTBA)),
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
			Source: c.Source, SourceKey: c.SourceKey, Kind: c.Kind,
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
	return []model.SnapshotLink{{Name: "VNDB", Link: "https://vndb.org/" + vndbID, Source: "vndb", SourceKey: "vndb"}}
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

	// Editable by the creator OR any holder of galgame.edit_any
	// (moderator/admin/ren) — the content-moderation edit power, matching the
	// role set the create/status gates already allow.
	if galgame.UserID != userID && !perm.Resolver.Can(roles, perm.EditAny) {
		return nil, errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	// PUT is "direct edit of a PUBLISHED entry" (docs 06/07). A submitter's
	// own pending(3)/declined(4) draft must go through PATCH/PatchDraft, which
	// flips declined→pending and emits the review-queue message. edit_any
	// holders keep direct-edit on any status (e.g. fixing a VNDB draft).
	if galgame.Status != model.GalgameStatusPublished && !perm.Resolver.Can(roles, perm.EditAny) {
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
	next := overlayUpdate(cur, req, vndbManagedTagIDs(full.Tag), vndbManagedOfficialIDs(full.Official))
	// ChangedKeys(cur, next) is the genuine edit delta — cur is live, so this
	// is exactly what the user touched, independent of any stale older
	// revision. Reused as both the no-op guard and the recorded changed_fields.
	changed := model.ChangedKeys(cur, next)
	if len(changed) == 0 {
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
		if err := tx.Model(&model.GalgameContributor{}).
			Where("galgame_id = ? AND user_id = ?", galgameID, userID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: galgameID, UserID: userID}).Error; err != nil {
				return err
			}
		}

		isMinor := false
		if req.IsMinor != nil {
			isMinor = *req.IsMinor
		}

		rev := &model.GalgameRevision{
			GalgameID: galgameID,
			Revision:  nextRev,
			UserID:    userID,
			Action:    "updated",
			Snapshot:  snapshotJSON,
			IsMinor:   isMinor,
		}
		rev.SetChangedFields(model.KeysOf(changed))
		return tx.Create(rev).Error
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
	pinned := s.pinnedCovers(ctx, galgames)
	items := make([]dto.GalgameBrief, len(galgames))
	for i := range galgames {
		items[i] = briefFromModel(&galgames[i], pinned)
	}
	s.enrichBriefValues(ctx, items)
	return items, nil
}

// EnrichBriefs maps an ALREADY-fetched, already-ordered page of galgames to
// enriched briefs (pinned cover + intrinsic image meta), preserving the caller's
// order. Unlike BatchGetWithViewer it does not re-query by id, so a relation page
// (official/tag → galgames, step 20) keeps its sort. Empty input → empty slice.
func (s *GalgameService) EnrichBriefs(ctx context.Context, galgames []model.Galgame) []dto.GalgameBrief {
	pinned := s.pinnedCovers(ctx, galgames)
	items := make([]dto.GalgameBrief, len(galgames))
	for i := range galgames {
		items[i] = briefFromModel(&galgames[i], pinned)
	}
	s.enrichBriefValues(ctx, items)
	return items
}

// pinnedCovers batches the pinned-cover (sort_order=0) hash lookup for a set of
// galgames into {id → image_hash}, avoiding O(N) per-row queries. Non-fatal on
// error (returns empty → briefs fall back to the legacy Banner URL). Shared by
// the brief + detail builders.
func (s *GalgameService) pinnedCovers(ctx context.Context, galgames []model.Galgame) map[int]string {
	ids := make([]int, 0, len(galgames))
	for i := range galgames {
		ids = append(ids, galgames[i].ID)
	}
	pinned, err := s.galgameRepo.PinnedCoverHashes(ctx, ids)
	if err != nil {
		return map[int]string{}
	}
	return pinned
}

// briefFromModel maps a galgame row + the batch's pinned-cover map to a
// GalgameBrief — the single source of truth for the brief shape (BatchGet,
// per-user lists, and the detail view all build on it).
func briefFromModel(g *model.Galgame, pinned map[int]string) dto.GalgameBrief {
	var effective *string
	if h, ok := pinned[g.ID]; ok {
		effective = &h
	}
	return dto.GalgameBrief{
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

// BatchDetailWithViewer returns rich detail briefs (brief + intro / officials /
// release date) for a list of IDs. Powers GET /galgame/batch?view=detail — e.g.
// the forum's "new Galgame" feed card. Heavier than BatchGet (intro text + an
// officials join); the lightweight brief path is left untouched.
func (s *GalgameService) BatchDetailWithViewer(ctx context.Context, ids []int, viewerUserID int, contentLimit string) ([]dto.GalgameDetailBrief, error) {
	galgames, err := s.galgameRepo.FindDetailByIDs(ctx, ids, viewerUserID, contentLimit)
	if err != nil {
		return nil, err
	}
	pinned := s.pinnedCovers(ctx, galgames)
	portraits := s.pinnedPortraits(ctx, galgames)

	resultIDs := make([]int, 0, len(galgames))
	for i := range galgames {
		resultIDs = append(resultIDs, galgames[i].ID)
	}
	officials, err := s.galgameRepo.OfficialNames(ctx, resultIDs)
	if err != nil {
		officials = map[int][]string{} // non-fatal: card just shows no maker line
	}

	items := make([]dto.GalgameDetailBrief, len(galgames))
	for i := range galgames {
		g := &galgames[i]
		releaseDate := ""
		if g.ReleaseDate != nil && !g.ReleaseDate.Time().IsZero() {
			releaseDate = g.ReleaseDate.Time().UTC().Format("2006-01-02")
		}
		names := officials[g.ID]
		if names == nil {
			names = []string{}
		}
		item := dto.GalgameDetailBrief{
			GalgameBrief:   briefFromModel(g, pinned),
			ReleaseDate:    releaseDate,
			ReleaseDateTBA: g.ReleaseDateTBA,
			IntroEnUS:      g.IntroEnUS,
			IntroJaJP:      g.IntroJaJP,
			IntroZhCN:      g.IntroZhCN,
			IntroZhTW:      g.IntroZhTW,
			Officials:      names,
			CatalogWorkID:  g.CatalogWorkID,
		}
		if h, ok := portraits[g.ID]; ok {
			item.EffectivePortraitHash = &h
		}
		items[i] = item
	}
	// Enrich the embedded briefs' banner metadata in one batched lookup.
	ptrs := make([]*dto.GalgameBrief, len(items))
	for i := range items {
		ptrs[i] = &items[i].GalgameBrief
	}
	s.enrichBriefBanners(ctx, ptrs)
	// Enrich the portrait pins' metadata in a second batched lookup so a
	// vertical card reserves the right aspect ratio with no layout shift.
	s.enrichDetailBriefPortraits(ctx, items)
	return items, nil
}

// pinnedPortraits batches the pinned-portrait (portrait_pinned) hash lookup for
// a set of galgames into {id → image_hash}. Non-fatal on error (empty map →
// briefs simply carry no portrait). Sibling of pinnedCovers.
func (s *GalgameService) pinnedPortraits(ctx context.Context, galgames []model.Galgame) map[int]string {
	ids := make([]int, 0, len(galgames))
	for i := range galgames {
		ids = append(ids, galgames[i].ID)
	}
	portraits, err := s.galgameRepo.PinnedPortraitHashes(ctx, ids)
	if err != nil {
		return map[int]string{}
	}
	return portraits
}

// enrichDetailBriefPortraits fills EffectivePortrait{Width,Height,Thumbhash} on
// the detail briefs from image_service in one batched lookup. No-op when
// enrichment is unwired or no brief carries a portrait pin.
func (s *GalgameService) enrichDetailBriefPortraits(ctx context.Context, items []dto.GalgameDetailBrief) {
	if s.imageMeta == nil || len(items) == 0 {
		return
	}
	hashes := make([]string, 0, len(items))
	for i := range items {
		if items[i].EffectivePortraitHash != nil {
			hashes = append(hashes, *items[i].EffectivePortraitHash)
		}
	}
	metas := s.resolveImageMeta(ctx, hashes)
	if len(metas) == 0 {
		return
	}
	for i := range items {
		if items[i].EffectivePortraitHash == nil {
			continue
		}
		if m, ok := metas[*items[i].EffectivePortraitHash]; ok {
			items[i].EffectivePortraitWidth = m.Width
			items[i].EffectivePortraitHeight = m.Height
			items[i].EffectivePortraitThumbhash = m.Thumbhash
		}
	}
}

// CheckVNDB checks if a VNDB ID already exists
func (s *GalgameService) CheckVNDB(ctx context.Context, vndbID string) (bool, int, error) {
	return s.galgameRepo.ExistsByVNDBID(ctx, vndbID)
}

// GetUserStats returns aggregated galgame statistics for a user
func (s *GalgameService) GetUserStats(ctx context.Context, userID int) (*dto.UserGalgameStats, error) {
	return s.galgameRepo.GetUserStats(ctx, userID)
}

// ListUserGalgames returns a user's PUBLISHED galgames as briefs (newest first,
// paginated) — the source for a consumer's profile "已发布 Galgame" tab. Mirrors
// BatchGetWithViewer's brief mapping (incl. the pinned-cover hash lookup).
func (s *GalgameService) ListUserGalgames(ctx context.Context, userID, page, limit int, contentLimit string) ([]dto.GalgameBrief, int64, error) {
	galgames, total, err := s.galgameRepo.ListPublishedByUser(ctx, userID, page, limit, contentLimit)
	if err != nil {
		return nil, 0, err
	}
	return s.galgameBriefsWithCovers(ctx, galgames), total, nil
}

// ListContributedGalgames returns the galgames a user has CONTRIBUTED to
// (created OR edited — the galgame_contributor relation) as briefs,
// newest-contribution-first, paginated. The source for a profile "贡献的
// Galgame" tab; a superset of ListUserGalgames (which is created-only).
// status=0 + NSFW gating are applied in the repo (public profile view).
func (s *GalgameService) ListContributedGalgames(ctx context.Context, userID, page, limit int, contentLimit string) ([]dto.GalgameBrief, int64, error) {
	galgames, total, err := s.galgameRepo.ListContributedByUser(ctx, userID, page, limit, contentLimit)
	if err != nil {
		return nil, 0, err
	}
	return s.galgameBriefsWithCovers(ctx, galgames), total, nil
}

// galgameBriefsWithCovers maps galgames → briefs, filling EffectiveBannerHash
// from the pinned-cover (sort_order=0) lookup. Shared by the per-user list
// endpoints (created / contributed); mirrors BatchGetWithViewer's brief mapping.
func (s *GalgameService) galgameBriefsWithCovers(ctx context.Context, galgames []model.Galgame) []dto.GalgameBrief {
	pinned := s.pinnedCovers(ctx, galgames)
	items := make([]dto.GalgameBrief, len(galgames))
	for i := range galgames {
		items[i] = briefFromModel(&galgames[i], pinned)
	}
	s.enrichBriefValues(ctx, items)
	return items
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
		Preload("Link", func(db *gorm.DB) *gorm.DB {
			// Deterministic order (insertion id) so the snapshot's Links —
			// compared order-sensitively by linksEqual — is stable across
			// reads; without it VNDB link re-sync would spuriously re-diff.
			return db.Order("id ASC")
		}).
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

// unionIDs returns the user ids plus any vndb-managed ids not already present
// (order-insensitive; TakeSnapshot/ChangedKeys canonical-sort). It's how an edit
// keeps the sync-managed relation subset it isn't allowed to drop.
func unionIDs(user, vndb []int) []int {
	seen := make(map[int]bool, len(user)+len(vndb))
	out := make([]int, 0, len(user)+len(vndb))
	for _, id := range user {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range vndb {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func vndbManagedTagIDs(rels []model.GalgameTagRelation) []int {
	var out []int
	for _, r := range rels {
		if r.Source == "vndb" {
			out = append(out, r.TagID)
		}
	}
	return out
}

func vndbManagedOfficialIDs(rels []model.GalgameOfficialRelation) []int {
	var out []int
	for _, r := range rels {
		if r.Source == "vndb" {
			out = append(out, r.OfficialID)
		}
	}
	return out
}

// overlayUpdate builds the *target snapshot* of a direct edit: it starts
// from the current canonical snapshot and overlays only the fields the
// request actually carries (pointer-presence semantics — nil leaves a
// field unchanged, a set value incl. empty string / empty slice is an
// authoritative replacement). This is the single place "what does this
// edit change" is expressed; repository.ApplySnapshot then writes it and
// the revision records exactly it. Every editable model.Snapshot field
// is reachable here (released / aliases / links / tag_ids / official_ids
// / engine_ids included). Sync-managed and carried over from `cur`, not
// user-editable: `bid` (BangumiID) entirely, and the source="vndb" subset
// of `links`, `tag_ids` and `official_ids` (the cron-synced store/official
// links + VNDB tags/developers — req replaces only the user subset, the
// vndb subset is unioned back; callers pass the current vndb-managed ids).
// See the invariant in docs/galgame_wiki/01-revision-system-design.md §1.5.
func overlayUpdate(cur *model.Snapshot, req *dto.UpdateGalgameRequest, vndbTagIDs, vndbOfficialIDs []int) *model.Snapshot {
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
		// presence: nil = keep; "" = clear to unknown; "YYYY[-MM[-DD]]" normalizes
		// (day-unknown → 1st of month, month-unknown → Jan 1). Validated upstream.
		n.ReleaseDate = model.NormalizeReleaseDateInput(*req.ReleaseDate)
	}
	if req.ReleaseDateTBA != nil {
		n.ReleaseDateTBA = *req.ReleaseDateTBA
	}
	// Re-derive precision only when this edit touched the date or tba; an
	// unrelated edit leaves the base snapshot's precision (carried from the model
	// by TakeSnapshot) intact, so it can't downgrade a month/year date to day.
	// When the date is in the patch, derive from the RAW input granularity
	// (n.ReleaseDate is already normalized and would read back as 'day').
	if req.ReleaseDate != nil {
		n.ReleasePrecision = string(model.DeriveInputPrecision(*req.ReleaseDate, n.ReleaseDateTBA))
	} else if req.ReleaseDateTBA != nil {
		switch {
		case n.ReleaseDateTBA:
			n.ReleasePrecision = string(model.PrecisionTBA)
		case n.ReleaseDate != nil:
			n.ReleasePrecision = string(model.PrecisionDay)
		default:
			n.ReleasePrecision = string(model.PrecisionUnknown)
		}
	}
	if req.SeriesID != nil {
		v := *req.SeriesID
		n.SeriesID = &v
	}
	if req.Aliases != nil {
		n.Aliases = append([]string(nil), (*req.Aliases)...)
	}
	if req.Links != nil {
		// req.Links authoritatively replaces the USER links only. The
		// VNDB-sourced links (source="vndb") are sync-managed — like bid — so
		// they're carried over from cur and merged back, deduped against any the
		// client echoed. Otherwise every user edit would wipe the synced
		// store/official links until the next cron. See mergeUserAndVndbLinks.
		user := make([]model.SnapshotLink, 0, len(*req.Links))
		for _, l := range *req.Links {
			user = append(user, model.SnapshotLink{Name: l.Name, Link: l.Link})
		}
		n.Links = mergeUserAndVndbLinks(user, vndbManagedLinks(cur.Links))
	}
	// tag_ids / official_ids: req replaces the USER subset, but the sync-managed
	// source="vndb" relations are carried over (like bid, and like vndb links) —
	// union them in so an edit can't drop them. reconcileSet is delta-based so the
	// kept rows retain their source column automatically. engine_ids has no vndb
	// provenance (engines are wiki-only), so it's a plain replacement.
	if req.TagIDs != nil {
		n.TagIDs = unionIDs(*req.TagIDs, vndbTagIDs)
	}
	if req.OfficialIDs != nil {
		n.OfficialIDs = unionIDs(*req.OfficialIDs, vndbOfficialIDs)
	}
	if req.EngineIDs != nil {
		n.EngineIDs = append([]int(nil), (*req.EngineIDs)...)
	}
	if req.Covers != nil {
		// req.Covers authoritatively replaces the USER covers only. The
		// VNDB-synced covers (source="vndb": the full /cv gallery — main +
		// release covers) are sync-managed like vndb links — carried over from
		// cur and merged back, deduped by image_hash. Otherwise a user setting
		// a custom banner would wipe the synced cover gallery. See
		// mergeUserAndVndbCovers (keeps at most one pinned sort_order=0).
		n.Covers = mergeUserAndVndbCovers(coverInputsToSnapshot(*req.Covers), vndbManagedCovers(cur.Covers))
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
//
// changedFields is the set of snapshot keys this operation actually changed —
// the caller knows it explicitly (e.g. the link/alias handlers change exactly
// "links" / "aliases"), which is more robust than re-deriving it here against a
// possibly-stale adjacent snapshot. Pass an empty slice for "no field change".
func (s *GalgameService) CreateRevisionFromCurrentState(tx *gorm.DB, galgameID, userID int, action, note string, isMinor bool, changedFields []string) error {
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

	rev := &model.GalgameRevision{
		GalgameID: galgameID,
		Revision:  nextRev,
		UserID:    userID,
		Action:    action,
		Note:      note,
		Snapshot:  snapshotJSON,
		IsMinor:   isMinor,
	}
	rev.SetChangedFields(changedFields)
	return tx.Create(rev).Error
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
