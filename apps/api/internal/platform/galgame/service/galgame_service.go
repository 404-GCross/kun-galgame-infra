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

// GetByID returns a galgame with all relations, enriched with user info
func (s *GalgameService) GetByID(ctx context.Context, id int) (*model.Galgame, map[int]*dto.UserBrief, error) {
	galgame, err := s.galgameRepo.FindByID(ctx, id)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, nil, err
	}

	if galgame.Status == 1 {
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

	updates := buildUpdates(req)
	if len(updates) == 0 {
		return galgame, nil
	}

	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Galgame{}).Where("id = ?", galgameID).Updates(updates).Error; err != nil {
			return err
		}

		// Get next revision number (with implicit row lock via MAX query)
		nextRev, err := repository.NextRevision(tx, galgameID)
		if err != nil {
			return err
		}

		// Take snapshot of the updated state
		fullGalgame, err := loadGalgameWithRelations(tx, galgameID)
		if err != nil {
			return err
		}
		snapshot := model.TakeSnapshot(fullGalgame)
		snapshotJSON, err := snapshot.ToJSON()
		if err != nil {
			return err
		}

		// Ensure editor is a contributor
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

// CheckVNDB checks if a VNDB ID already exists
func (s *GalgameService) CheckVNDB(ctx context.Context, vndbID string) (bool, int, error) {
	return s.galgameRepo.ExistsByVNDBID(ctx, vndbID)
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

func buildUpdates(req *dto.UpdateGalgameRequest) map[string]any {
	u := map[string]any{}
	if req.VNDBID != nil {
		u["vndb_id"] = *req.VNDBID
	}
	if req.NameEnUS != nil {
		u["name_en_us"] = *req.NameEnUS
	}
	if req.NameJaJP != nil {
		u["name_ja_jp"] = *req.NameJaJP
	}
	if req.NameZhCN != nil {
		u["name_zh_cn"] = *req.NameZhCN
	}
	if req.NameZhTW != nil {
		u["name_zh_tw"] = *req.NameZhTW
	}
	if req.Banner != nil {
		u["banner"] = *req.Banner
	}
	if req.IntroEnUS != nil {
		u["intro_en_us"] = *req.IntroEnUS
	}
	if req.IntroJaJP != nil {
		u["intro_ja_jp"] = *req.IntroJaJP
	}
	if req.IntroZhCN != nil {
		u["intro_zh_cn"] = *req.IntroZhCN
	}
	if req.IntroZhTW != nil {
		u["intro_zh_tw"] = *req.IntroZhTW
	}
	if req.ContentLimit != nil {
		u["content_limit"] = *req.ContentLimit
	}
	if req.OriginalLanguage != nil {
		u["original_language"] = *req.OriginalLanguage
	}
	if req.AgeLimit != nil {
		u["age_limit"] = *req.AgeLimit
	}
	if req.SeriesID != nil {
		u["series_id"] = *req.SeriesID
	}
	return u
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

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}
