package service

import (
	"context"

	"api/internal/platform/content/model"
	"api/internal/platform/content/repository"
	"api/pkg/utils"
)

// ContentService handles content business logic
type ContentService struct {
	contentRepo *repository.ContentRepository
}

// NewContentService creates a new ContentService
func NewContentService(contentRepo *repository.ContentRepository) *ContentService {
	return &ContentService{contentRepo: contentRepo}
}

// GetByID gets content by ID
func (s *ContentService) GetByID(ctx context.Context, id uint) (*model.Content, error) {
	return s.contentRepo.FindByID(ctx, id)
}

// GetByUUID gets content by UUID
func (s *ContentService) GetByUUID(ctx context.Context, uuid string) (*model.Content, error) {
	return s.contentRepo.FindByUUID(ctx, uuid)
}

// Create creates new content
func (s *ContentService) Create(ctx context.Context, content *model.Content) error {
	return s.contentRepo.Create(ctx, content)
}

// Update updates content
func (s *ContentService) Update(ctx context.Context, content *model.Content) error {
	return s.contentRepo.Update(ctx, content)
}

// Delete deletes content
func (s *ContentService) Delete(ctx context.Context, id uint) error {
	return s.contentRepo.Delete(ctx, id)
}

// List lists content with pagination
func (s *ContentService) List(ctx context.Context, siteID uint, p utils.Pagination) (utils.PaginatedResult[model.Content], error) {
	p.Normalize()
	contents, total, err := s.contentRepo.List(ctx, siteID, p.Offset(), p.Limit())
	if err != nil {
		return utils.PaginatedResult[model.Content]{}, err
	}
	return utils.NewPaginatedResult(contents, total, p.Page, p.PageSize), nil
}
