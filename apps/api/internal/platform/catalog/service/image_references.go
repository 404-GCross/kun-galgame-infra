package service

import (
	"context"

	"api/internal/platform/catalog/imagerefs"

	"gorm.io/gorm"
)

// ImageReferenceService answers "which catalog rows point at this image hash",
// and can release them. It exists for the admin console's delete flow: the
// image service keys bytes by hash and catalog keys references by row, so
// deleting bytes that a catalog row still names leaves a blank frame that
// becomes permanent once the 30-day GC window closes.
//
// The registry it reads is internal/platform/catalog/imagerefs — the same one
// the daily keep-alive and reconcile sweeps consume, so what the console shows
// an operator is exactly what the jobs count.
type ImageReferenceService struct {
	db *gorm.DB
}

func NewImageReferenceService(db *gorm.DB) *ImageReferenceService {
	return &ImageReferenceService{db: db}
}

// List returns the references to one hash, with the owning entity's name.
// An unreferenced hash is an empty list, not an error — that is the console's
// ordinary case (most images have no catalog row at all).
func (s *ImageReferenceService) List(ctx context.Context, hash string) ([]imagerefs.Ref, error) {
	return imagerefs.CollectByHash(ctx, s.db, hash)
}

// Detach releases every catalog reference to a hash and reports the rows
// affected per kind. It touches catalog only: the bytes are the image
// service's to delete, and the console does that as a separate step.
func (s *ImageReferenceService) Detach(ctx context.Context, hash string) (map[string]int64, error) {
	return imagerefs.Detach(ctx, s.db, hash)
}
