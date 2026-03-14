package repository

import (
	"context"

	"api/internal/platform/moderation/model"

	"gorm.io/gorm"
)

// ModerationRepository handles moderation data access
type ModerationRepository struct {
	db *gorm.DB
}

// NewModerationRepository creates a new ModerationRepository
func NewModerationRepository(db *gorm.DB) *ModerationRepository {
	return &ModerationRepository{db: db}
}

// FindJobByID finds a job by ID
func (r *ModerationRepository) FindJobByID(ctx context.Context, id uint) (*model.Job, error) {
	var job model.Job
	if err := r.db.WithContext(ctx).First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// FindJobByUUID finds a job by UUID
func (r *ModerationRepository) FindJobByUUID(ctx context.Context, uuid string) (*model.Job, error) {
	var job model.Job
	if err := r.db.WithContext(ctx).Where("uuid = ?", uuid).First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// CreateJob creates a new job
func (r *ModerationRepository) CreateJob(ctx context.Context, job *model.Job) error {
	return r.db.WithContext(ctx).Create(job).Error
}

// UpdateJob updates a job
func (r *ModerationRepository) UpdateJob(ctx context.Context, job *model.Job) error {
	return r.db.WithContext(ctx).Save(job).Error
}

// ListPendingJobs lists pending jobs
func (r *ModerationRepository) ListPendingJobs(ctx context.Context, limit int) ([]model.Job, error) {
	var jobs []model.Job
	if err := r.db.WithContext(ctx).Where("status = ?", "pending").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// ListJobs lists jobs with pagination
func (r *ModerationRepository) ListJobs(ctx context.Context, offset, limit int) ([]model.Job, int64, error) {
	var jobs []model.Job
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.Job{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Order("created_at DESC").Find(&jobs).Error; err != nil {
		return nil, 0, err
	}

	return jobs, total, nil
}

// CreateResult creates a new result
func (r *ModerationRepository) CreateResult(ctx context.Context, result *model.Result) error {
	return r.db.WithContext(ctx).Create(result).Error
}
