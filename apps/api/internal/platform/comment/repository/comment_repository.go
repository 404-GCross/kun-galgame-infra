package repository

import (
	"context"

	"api/internal/platform/comment/model"

	"gorm.io/gorm"
)

// CommentRepository handles comment data access
type CommentRepository struct {
	db *gorm.DB
}

// NewCommentRepository creates a new CommentRepository
func NewCommentRepository(db *gorm.DB) *CommentRepository {
	return &CommentRepository{db: db}
}

// FindByID finds a comment by ID
func (r *CommentRepository) FindByID(ctx context.Context, id uint) (*model.Comment, error) {
	var comment model.Comment
	if err := r.db.WithContext(ctx).First(&comment, id).Error; err != nil {
		return nil, err
	}
	return &comment, nil
}

// Create creates a new comment
func (r *CommentRepository) Create(ctx context.Context, comment *model.Comment) error {
	return r.db.WithContext(ctx).Create(comment).Error
}

// Update updates a comment
func (r *CommentRepository) Update(ctx context.Context, comment *model.Comment) error {
	return r.db.WithContext(ctx).Save(comment).Error
}

// Delete soft deletes a comment
func (r *CommentRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&model.Comment{}, id).Error
}

// ListByContentUUID lists comments by content UUID
func (r *CommentRepository) ListByContentUUID(ctx context.Context, contentUUID string, offset, limit int) ([]model.Comment, int64, error) {
	var comments []model.Comment
	var total int64

	q := r.db.WithContext(ctx).Model(&model.Comment{}).Where("content_uuid = ? AND parent_id IS NULL", contentUUID)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Preload("Replies").Offset(offset).Limit(limit).Order("created_at DESC").Find(&comments).Error; err != nil {
		return nil, 0, err
	}

	return comments, total, nil
}
