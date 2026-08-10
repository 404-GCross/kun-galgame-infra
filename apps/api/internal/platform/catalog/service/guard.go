package service

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

type GuardService struct {
	db *gorm.DB
}

func NewGuardService(db *gorm.DB) *GuardService {
	return &GuardService{db: db}
}

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
