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

// GalgameService handles galgame business logic
type GalgameService struct {
	galgameRepo  *repository.GalgameRepository
	revisionRepo *repository.RevisionRepository
	prRepo       *repository.PRRepository
	userRepo     *repository.UserReadonlyRepository
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

	var galgame model.Galgame

	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		galgame = model.Galgame{
			VNDBID:           req.VNDBID,
			NameEnUS:         req.NameEnUS,
			NameJaJP:         req.NameJaJP,
			NameZhCN:         req.NameZhCN,
			NameZhTW:         req.NameZhTW,
			Banner:           req.Banner,
			BannerImageHash:  strToPtr(req.BannerImageHash),
			IntroEnUS:        req.IntroEnUS,
			IntroJaJP:        req.IntroJaJP,
			IntroZhCN:        req.IntroZhCN,
			IntroZhTW:        req.IntroZhTW,
			ContentLimit:     req.ContentLimit,
			OriginalLanguage: req.OriginalLanguage,
			AgeLimit:         req.AgeLimit,
			UserID:           uid,
			SeriesID:         req.SeriesID,
		}
		if galgame.ContentLimit == "" {
			galgame.ContentLimit = "sfw"
		}
		if galgame.AgeLimit == "" {
			galgame.AgeLimit = "r18"
		}
		if galgame.OriginalLanguage == "" {
			galgame.OriginalLanguage = "ja-jp"
		}
		if err := tx.Create(&galgame).Error; err != nil {
			return err
		}

		// Aliases
		if req.Aliases != "" {
			for _, name := range strings.Split(req.Aliases, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if err := tx.Create(&model.GalgameAlias{GalgameID: galgame.ID, Name: name}).Error; err != nil {
					return err
				}
			}
		}

		// Tag relations
		for _, tagID := range req.TagIDs {
			if err := tx.Create(&model.GalgameTagRelation{GalgameID: galgame.ID, TagID: tagID}).Error; err != nil {
				return err
			}
		}

		// Official relations
		for _, officialID := range req.OfficialIDs {
			if err := tx.Create(&model.GalgameOfficialRelation{GalgameID: galgame.ID, OfficialID: officialID}).Error; err != nil {
				return err
			}
		}

		// Engine relations
		for _, engineID := range req.EngineIDs {
			if err := tx.Create(&model.GalgameEngineRelation{GalgameID: galgame.ID, EngineID: engineID}).Error; err != nil {
				return err
			}
		}

		// Contributor
		if err := tx.Create(&model.GalgameContributor{GalgameID: galgame.ID, UserID: uid}).Error; err != nil {
			return err
		}

		// VNDB link
		if err := tx.Create(&model.GalgameLink{GalgameID: galgame.ID, UserID: uid, Name: "VNDB", Link: "https://vndb.org/" + req.VNDBID}).Error; err != nil {
			return err
		}

		// Take snapshot and create revision 1
		fullGalgame, err := loadGalgameWithRelations(tx, galgame.ID)
		if err != nil {
			return err
		}
		snapshot := model.TakeSnapshot(fullGalgame)
		snapshotJSON, err := snapshot.ToJSON()
		if err != nil {
			return err
		}

		return tx.Create(&model.GalgameRevision{
			GalgameID: galgame.ID,
			Revision:  1,
			UserID:    uid,
			Action:    "created",
			Snapshot:  snapshotJSON,
		}).Error
	})

	if err != nil {
		return nil, err
	}

	return &galgame, nil
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

	items := make([]dto.GalgameBrief, len(galgames))
	for i, g := range galgames {
		items[i] = dto.GalgameBrief{
			ID:                 g.ID,
			VNDBID:             g.VNDBID,
			NameEnUS:           g.NameEnUS,
			NameJaJP:           g.NameJaJP,
			NameZhCN:           g.NameZhCN,
			NameZhTW:           g.NameZhTW,
			Banner:             g.Banner,
			BannerImageHash:    g.BannerImageHash,
			ContentLimit:       g.ContentLimit,
			Status:             g.Status,
			UserID:             g.UserID,
			ResourceUpdateTime: g.ResourceUpdateTime.Format("2006-01-02T15:04:05Z"),
			OriginalLanguage:   g.OriginalLanguage,
			AgeLimit:           g.AgeLimit,
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

// loadGalgameWithRelations loads a galgame with all relations using the given tx
func loadGalgameWithRelations(tx *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := tx.
		Preload("Alias").
		Preload("Tag").
		Preload("Official").
		Preload("Engine").
		Preload("Link").
		First(&g, id).Error
	return &g, err
}

// overlayUpdate builds the *target snapshot* of a direct edit: it starts
// from the current canonical snapshot and overlays only the fields the
// request actually carries (pointer-presence semantics — nil leaves a
// field unchanged, a set value incl. empty string / empty slice is an
// authoritative replacement). This is the single place "what does this
// edit change" is expressed; repository.ApplySnapshot then writes it and
// the revision records exactly it. Fields NOT modelled on
// UpdateGalgameRequest (aliases / links — they have their own per-item
// revision endpoints) are carried over from `cur` untouched, so a
// galgame edit never disturbs them. See
// docs/galgame_wiki/01-revision-system-design.md §"统一编辑路径".
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
	if req.SeriesID != nil {
		v := *req.SeriesID
		n.SeriesID = &v
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
