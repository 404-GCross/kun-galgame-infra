package service

import (
	"context"

	"api/internal/platform/comment/model"
	"api/internal/platform/comment/repository"
	"api/pkg/utils"
)

// CommentService handles comment business logic
type CommentService struct {
	commentRepo *repository.CommentRepository
}

// NewCommentService creates a new CommentService
func NewCommentService(commentRepo *repository.CommentRepository) *CommentService {
	return &CommentService{commentRepo: commentRepo}
}

// GetByID gets a comment by ID
func (s *CommentService) GetByID(ctx context.Context, id uint) (*model.Comment, error) {
	return s.commentRepo.FindByID(ctx, id)
}

// Create creates a new comment
func (s *CommentService) Create(ctx context.Context, comment *model.Comment) error {
	return s.commentRepo.Create(ctx, comment)
}

// Delete deletes a comment
func (s *CommentService) Delete(ctx context.Context, id uint) error {
	return s.commentRepo.Delete(ctx, id)
}

// UpdateStatus updates comment status
func (s *CommentService) UpdateStatus(ctx context.Context, id uint, status int) error {
	comment, err := s.commentRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	comment.Status = status
	return s.commentRepo.Update(ctx, comment)
}

// ListByContent lists comments by content UUID
func (s *CommentService) ListByContent(ctx context.Context, contentUUID string, p utils.Pagination) (utils.PaginatedResult[model.Comment], error) {
	p.Normalize()
	comments, total, err := s.commentRepo.ListByContentUUID(ctx, contentUUID, p.Offset(), p.Limit())
	if err != nil {
		return utils.PaginatedResult[model.Comment]{}, err
	}
	return utils.NewPaginatedResult(comments, total, p.Page, p.PageSize), nil
}
