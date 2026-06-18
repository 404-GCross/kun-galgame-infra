package repository

import (
	"context"
	"time"

	"api/internal/platform/auth/model"

	"gorm.io/gorm"
)

// CreatorApplicationRepository is the data access for creator role applications.
type CreatorApplicationRepository struct {
	db *gorm.DB
}

func NewCreatorApplicationRepository(db *gorm.DB) *CreatorApplicationRepository {
	return &CreatorApplicationRepository{db: db}
}

func (r *CreatorApplicationRepository) Create(ctx context.Context, app *model.CreatorApplication) error {
	return r.db.WithContext(ctx).Create(app).Error
}

// FindLatestByUser returns the user's most recent application (any status), or
// gorm.ErrRecordNotFound if they've never applied.
func (r *CreatorApplicationRepository) FindLatestByUser(ctx context.Context, userID uint) (*model.CreatorApplication, error) {
	var app model.CreatorApplication
	if err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).Order("id DESC").First(&app).Error; err != nil {
		return nil, err
	}
	return &app, nil
}

func (r *CreatorApplicationRepository) FindByID(ctx context.Context, id uint) (*model.CreatorApplication, error) {
	var app model.CreatorApplication
	if err := r.db.WithContext(ctx).First(&app, id).Error; err != nil {
		return nil, err
	}
	return &app, nil
}

func (r *CreatorApplicationRepository) ListByStatus(ctx context.Context, status string, page, limit int) ([]model.CreatorApplication, int64, error) {
	var items []model.CreatorApplication
	var total int64
	q := r.db.WithContext(ctx).Model(&model.CreatorApplication{})
	if status != "" {
		q = q.Where("status = ?", status)
	}
	q.Count(&total)
	err := q.Order("id DESC").Offset((page - 1) * limit).Limit(limit).Find(&items).Error
	return items, total, err
}

// Review atomically transitions a PENDING application to approved/declined and
// stamps the reviewer + time. Returns rows affected: 0 means it was already
// reviewed (lost a concurrent race) — the caller treats that as not-pending.
func (r *CreatorApplicationRepository) Review(ctx context.Context, id uint, status string, reviewerID uint, reason string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.CreatorApplication{}).
		Where("id = ? AND status = ?", id, model.CreatorAppPending).
		Updates(map[string]any{
			"status":         status,
			"reviewer_id":    reviewerID,
			"reviewed_at":    time.Now(),
			"decline_reason": reason,
		})
	return res.RowsAffected, res.Error
}
