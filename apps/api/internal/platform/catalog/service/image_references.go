package service

import (
	"context"

	"api/internal/platform/catalog/imagerefs"

	"gorm.io/gorm"
)

type ImageReferenceService struct {
	db *gorm.DB
}

func NewImageReferenceService(db *gorm.DB) *ImageReferenceService {
	return &ImageReferenceService{db: db}
}

func (s *ImageReferenceService) List(ctx context.Context, hash string) ([]imagerefs.Ref, error) {
	return imagerefs.CollectByHash(ctx, s.db, hash)
}

func (s *ImageReferenceService) Detach(ctx context.Context, hash string) (map[string]int64, error) {
	return imagerefs.Detach(ctx, s.db, hash)
}
