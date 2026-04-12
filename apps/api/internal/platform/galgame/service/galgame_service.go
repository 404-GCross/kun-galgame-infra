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
	galgameRepo *repository.GalgameRepository
	userRepo    *repository.UserReadonlyRepository
}

// NewGalgameService creates a new GalgameService
func NewGalgameService(galgameRepo *repository.GalgameRepository, userRepo *repository.UserReadonlyRepository) *GalgameService {
	return &GalgameService{
		galgameRepo: galgameRepo,
		userRepo:    userRepo,
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

	// Collect unique user IDs for batch lookup
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

	// Increment view asynchronously (fire-and-forget)
	go func() {
		_ = s.galgameRepo.IncrementView(context.Background(), id)
	}()

	return galgame, users, nil
}

// Create creates a new galgame with all related entities in a transaction
func (s *GalgameService) Create(ctx context.Context, uid int, req *dto.CreateGalgameRequest) (*model.Galgame, error) {
	// Validate VNDB ID format
	if !vndbIDRegex.MatchString(req.VNDBID) {
		return nil, errors.NewWithCode(errors.ErrGalgameInvalidVNDB)
	}

	// Check VNDB ID uniqueness
	exists, _, err := s.galgameRepo.ExistsByVNDBID(ctx, req.VNDBID)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.NewWithCode(errors.ErrGalgameVNDBExists)
	}

	var galgame model.Galgame

	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Create main galgame record
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

		// Create aliases
		if req.Aliases != "" {
			aliases := strings.Split(req.Aliases, ",")
			for _, name := range aliases {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if err := tx.Create(&model.GalgameAlias{
					GalgameID: galgame.ID,
					Name:      name,
				}).Error; err != nil {
					return err
				}
			}
		}

		// Create tag relations
		for _, tagID := range req.TagIDs {
			if err := tx.Create(&model.GalgameTagRelation{
				GalgameID: galgame.ID,
				TagID:     tagID,
			}).Error; err != nil {
				return err
			}
		}

		// Create official relations
		for _, officialID := range req.OfficialIDs {
			if err := tx.Create(&model.GalgameOfficialRelation{
				GalgameID:  galgame.ID,
				OfficialID: officialID,
			}).Error; err != nil {
				return err
			}
		}

		// Create engine relations
		for _, engineID := range req.EngineIDs {
			if err := tx.Create(&model.GalgameEngineRelation{
				GalgameID: galgame.ID,
				EngineID:  engineID,
			}).Error; err != nil {
				return err
			}
		}

		// Add creator as contributor
		if err := tx.Create(&model.GalgameContributor{
			GalgameID: galgame.ID,
			UserID:    uid,
		}).Error; err != nil {
			return err
		}

		// Create history record
		if err := tx.Create(&model.GalgameHistory{
			GalgameID: galgame.ID,
			UserID:    uid,
			Action:    "created",
			Type:      "galgame",
		}).Error; err != nil {
			return err
		}

		// Create VNDB link
		if err := tx.Create(&model.GalgameLink{
			GalgameID: galgame.ID,
			UserID:    uid,
			Name:      "VNDB",
			Link:      "https://vndb.org/" + req.VNDBID,
		}).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &galgame, nil
}

// Update updates a galgame (only by creator or admin)
func (s *GalgameService) Update(ctx context.Context, uid, galgameID int, roles []string, req *dto.UpdateGalgameRequest) (*model.Galgame, error) {
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
		}
		return nil, err
	}

	// Check permission: creator or admin
	isAdmin := false
	for _, r := range roles {
		if r == "admin" {
			isAdmin = true
			break
		}
	}
	if galgame.UserID != uid && !isAdmin {
		return nil, errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	// Apply updates
	updates := map[string]any{}
	if req.VNDBID != nil {
		updates["vndb_id"] = *req.VNDBID
	}
	if req.NameEnUS != nil {
		updates["name_en_us"] = *req.NameEnUS
	}
	if req.NameJaJP != nil {
		updates["name_ja_jp"] = *req.NameJaJP
	}
	if req.NameZhCN != nil {
		updates["name_zh_cn"] = *req.NameZhCN
	}
	if req.NameZhTW != nil {
		updates["name_zh_tw"] = *req.NameZhTW
	}
	if req.Banner != nil {
		updates["banner"] = *req.Banner
	}
	if req.IntroEnUS != nil {
		updates["intro_en_us"] = *req.IntroEnUS
	}
	if req.IntroJaJP != nil {
		updates["intro_ja_jp"] = *req.IntroJaJP
	}
	if req.IntroZhCN != nil {
		updates["intro_zh_cn"] = *req.IntroZhCN
	}
	if req.IntroZhTW != nil {
		updates["intro_zh_tw"] = *req.IntroZhTW
	}
	if req.ContentLimit != nil {
		updates["content_limit"] = *req.ContentLimit
	}
	if req.OriginalLanguage != nil {
		updates["original_language"] = *req.OriginalLanguage
	}
	if req.AgeLimit != nil {
		updates["age_limit"] = *req.AgeLimit
	}
	if req.SeriesID != nil {
		updates["series_id"] = *req.SeriesID
	}

	if len(updates) == 0 {
		return galgame, nil
	}

	err = s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Galgame{}).Where("id = ?", galgameID).Updates(updates).Error; err != nil {
			return err
		}

		// Create history record
		return tx.Create(&model.GalgameHistory{
			GalgameID: galgameID,
			UserID:    uid,
			Action:    "updated",
			Type:      "galgame",
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
