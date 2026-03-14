package service

import (
	"context"

	"api/internal/platform/artifact/model"
	"api/internal/platform/artifact/repository"
	"api/pkg/utils"
)

// ArtifactService handles artifact business logic
type ArtifactService struct {
	artifactRepo *repository.ArtifactRepository
}

// NewArtifactService creates a new ArtifactService
func NewArtifactService(artifactRepo *repository.ArtifactRepository) *ArtifactService {
	return &ArtifactService{artifactRepo: artifactRepo}
}

// GetByID gets an artifact by ID
func (s *ArtifactService) GetByID(ctx context.Context, id uint) (*model.Artifact, error) {
	return s.artifactRepo.FindByID(ctx, id)
}

// GetByUUID gets an artifact by UUID
func (s *ArtifactService) GetByUUID(ctx context.Context, uuid string) (*model.Artifact, error) {
	return s.artifactRepo.FindByUUID(ctx, uuid)
}

// Create creates a new artifact
func (s *ArtifactService) Create(ctx context.Context, artifact *model.Artifact) error {
	return s.artifactRepo.Create(ctx, artifact)
}

// Update updates an artifact
func (s *ArtifactService) Update(ctx context.Context, artifact *model.Artifact) error {
	return s.artifactRepo.Update(ctx, artifact)
}

// Delete deletes an artifact
func (s *ArtifactService) Delete(ctx context.Context, id uint) error {
	return s.artifactRepo.Delete(ctx, id)
}

// List lists artifacts with pagination
func (s *ArtifactService) List(ctx context.Context, p utils.Pagination) (utils.PaginatedResult[model.Artifact], error) {
	p.Normalize()
	artifacts, total, err := s.artifactRepo.List(ctx, p.Offset(), p.Limit())
	if err != nil {
		return utils.PaginatedResult[model.Artifact]{}, err
	}
	return utils.NewPaginatedResult(artifacts, total, p.Page, p.PageSize), nil
}

// Publish publishes an artifact
func (s *ArtifactService) Publish(ctx context.Context, id uint) error {
	return s.artifactRepo.UpdateStatus(ctx, id, 1)
}
