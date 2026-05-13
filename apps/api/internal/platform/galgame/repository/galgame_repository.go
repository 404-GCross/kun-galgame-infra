package repository

import (
	"context"
	"strings"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GalgameRepository handles galgame data access on kun_galgame_wiki
type GalgameRepository struct {
	db *gorm.DB
}

// NewGalgameRepository creates a new GalgameRepository
func NewGalgameRepository(db *gorm.DB) *GalgameRepository {
	return &GalgameRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions
func (r *GalgameRepository) DB() *gorm.DB {
	return r.db
}

// FindByID finds a galgame by ID with all relations
func (r *GalgameRepository) FindByID(ctx context.Context, id int) (*model.Galgame, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Preload("Alias").
		Preload("Series").
		Preload("Contributor").
		Preload("Link").
		Preload("Tag.Tag").
		Preload("Official.Official").
		Preload("Official.Official.Alias").
		Preload("Engine.Engine").
		First(&galgame, id).Error
	if err != nil {
		return nil, err
	}
	return &galgame, nil
}

// ExistsByVNDBID checks if a galgame with the given VNDB ID exists
func (r *GalgameRepository) ExistsByVNDBID(ctx context.Context, vndbID string) (bool, int, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Select("id").
		Where("vndb_id = ?", vndbID).
		First(&galgame).Error
	if err == gorm.ErrRecordNotFound {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, galgame.ID, nil
}

// List returns a paginated list of galgames
func (r *GalgameRepository) List(ctx context.Context, page, limit int, sortField, sortOrder, search string) ([]model.Galgame, int64, error) {
	var items []model.Galgame
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Galgame{}).Where("status = 0")

	if search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where(
			"LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ?",
			like, like, like, like,
		)
	}

	query.Count(&total)

	// Whitelist allowed sort fields (snake_case column names only)
	allowedSortFields := map[string]bool{
		"created":              true,
		"updated":              true,
		"view":                 true,
		"resource_update_time": true,
	}
	if !allowedSortFields[sortField] {
		sortField = "created"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}
	order := sortField + " " + sortOrder

	err := query.
		Order(order).
		Offset((page - 1) * limit).
		Limit(limit).
		Preload("Tag.Tag").
		Preload("Official.Official").
		Find(&items).Error

	return items, total, err
}

// FindByIDs finds galgames by a list of IDs (lightweight, no relations)
func (r *GalgameRepository) FindByIDs(ctx context.Context, ids []int) ([]model.Galgame, error) {
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, content_limit, user_id, resource_update_time, original_language, age_limit").
		Where("id IN ? AND status = 0", ids).
		Find(&galgames).Error
	return galgames, err
}

// FindByIDsAny is like FindByIDs but does NOT filter by status. Used by
// internal services (e.g. MessageService.enrich) that already authorize the
// audience and need the row regardless of state. Public API must use
// FindByIDs or FindByIDsWithViewer instead.
func (r *GalgameRepository) FindByIDsAny(ctx context.Context, ids []int) ([]model.Galgame, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, banner_image_hash, content_limit, status, user_id, resource_update_time, original_language, age_limit").
		Where("id IN ?", ids).
		Find(&galgames).Error
	return galgames, err
}

// FindByIDsWithViewer is like FindByIDs but additionally returns entries
// in status {3, 4} where the viewer is the submitter. Used by
// GET /galgame/batch when the caller authenticates with a user JWT.
//
// viewerUID == 0 falls back to FindByIDs (public visibility).
func (r *GalgameRepository) FindByIDsWithViewer(ctx context.Context, ids []int, viewerUID int) ([]model.Galgame, error) {
	if viewerUID <= 0 {
		return r.FindByIDs(ctx, ids)
	}
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, content_limit, status, user_id, resource_update_time, original_language, age_limit").
		Where("id IN ?", ids).
		Where("status = 0 OR (status IN (3, 4) AND user_id = ?)", viewerUID).
		Find(&galgames).Error
	return galgames, err
}

// ListMine returns galgames the user has submitted matching one of the
// given statuses. Used by GET /galgame/mine.
func (r *GalgameRepository) ListMine(ctx context.Context, uid int, statuses []int, page, limit int) ([]model.Galgame, int64, error) {
	var items []model.Galgame
	var total int64

	q := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ?", uid).
		Where("status IN ?", statuses)

	q.Count(&total)

	err := q.Order("updated DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error
	return items, total, err
}

// CheckSubmitQuota counts submissions made today by this user.
// Caller compares against the configured per-day limit (default 5).
func (r *GalgameRepository) CountSubmissionsToday(ctx context.Context, uid int) (int64, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var cnt int64
	err := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status IN (3, 4) AND created >= ?", uid, dayStart).
		Count(&cnt).Error
	return cnt, err
}

// FindForUpdate selects a galgame with row-level locking. Used in claim/patch
// to avoid race conditions between concurrent admins/users.
func (r *GalgameRepository) FindForUpdate(tx *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// Create creates a galgame record
func (r *GalgameRepository) Create(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Create(galgame).Error
}

// Update updates a galgame record
func (r *GalgameRepository) Update(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Save(galgame).Error
}

// IncrementView increments the view count
func (r *GalgameRepository) IncrementView(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id = ?", id).
		Update("view", gorm.Expr("view + 1")).Error
}

// GetUserStats returns aggregated galgame statistics for a user
func (r *GalgameRepository) GetUserStats(ctx context.Context, uid int) (*dto.UserGalgameStats, error) {
	var stats dto.UserGalgameStats
	var cnt int64

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Galgames created (total)
	r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0", uid).Count(&cnt)
	stats.GalgameCreated = int(cnt)

	// Galgames created today
	r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0 AND created >= ?", uid, todayStart).Count(&cnt)
	stats.GalgameCreatedToday = int(cnt)

	// Galgames contributed to (distinct galgame_id)
	r.db.WithContext(ctx).Model(&model.GalgameContributor{}).
		Where("user_id = ?", uid).Distinct("galgame_id").Count(&cnt)
	stats.GalgameContributed = int(cnt)

	// Revision count
	r.db.WithContext(ctx).Model(&model.GalgameRevision{}).
		Where("user_id = ?", uid).Count(&cnt)
	stats.RevisionCount = int(cnt)

	// PR submitted (total)
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ?", uid).Count(&cnt)
	stats.PRSubmitted = int(cnt)

	// PR merged
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 1", uid).Count(&cnt)
	stats.PRMerged = int(cnt)

	// PR declined
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 2", uid).Count(&cnt)
	stats.PRDeclined = int(cnt)

	// PR pending
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 0", uid).Count(&cnt)
	stats.PRPending = int(cnt)

	return &stats, nil
}
