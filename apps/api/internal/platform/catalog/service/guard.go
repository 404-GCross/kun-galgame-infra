package service

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// GuardService hosts the cross-cutting integrity guards.
type GuardService struct {
	db *gorm.DB
}

func NewGuardService(db *gorm.DB) *GuardService {
	return &GuardService{db: db}
}

// AssertDeletable enforces doc 10 invariant 8: an entity with any site usage
// must never be hard-deleted — only merged (which leaves a redirect, so
// references keep resolving) or soft-deleted. Every future hard-delete entry
// point must pass through this guard; the merge path is exempt by design
// (retiring the source behind a redirect is not a deletion).
func (g *GuardService) AssertDeletable(ctx context.Context, entityType int16, id int64) error {
	used, err := repository.HasUsage(g.db.WithContext(ctx), entityType, id)
	if err != nil {
		return err
	}
	if used {
		return fmt.Errorf("%w: entity type %d id %d", ErrHasUsage, entityType, id)
	}
	return nil
}
