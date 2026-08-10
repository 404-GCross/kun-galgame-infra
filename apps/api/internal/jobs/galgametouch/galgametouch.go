package galgametouch

import (
	"context"
	"fmt"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"
	"api/pkg/config"

	"gorm.io/gorm"
)

const siteGalgameWiki = "galgame_wiki"

const mapChunk = 2000

type Toucher struct {
	conn    *database.PostgresDB
	db      *gorm.DB
	touched int
}

func Open(cfg *config.Config) (*Toucher, error) {
	conn, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db (%s): %w", cfg.CatalogDatabase.DBName, err)
	}
	return &Toucher{conn: conn, db: conn.DB()}, nil
}

func New(db *gorm.DB) *Toucher { return &Toucher{db: db} }

func (t *Toucher) Close() {
	if t == nil || t.conn == nil {
		return
	}
	_ = t.conn.Close()
}

func (t *Toucher) Count() int {
	if t == nil {
		return 0
	}
	return t.touched
}

func (t *Toucher) Touch(ctx context.Context, galgameIDs []int) error {
	if t == nil || len(galgameIDs) == 0 {
		return nil
	}
	workIDs, err := t.resolve(ctx, galgameIDs)
	if err != nil {
		return err
	}
	if err := repository.TouchWorks(ctx, t.db, workIDs); err != nil {
		return fmt.Errorf("touch claimed works: %w", err)
	}
	t.touched += len(workIDs)
	return nil
}

func (t *Toucher) resolve(ctx context.Context, galgameIDs []int) ([]int64, error) {
	seen := make(map[int64]struct{}, len(galgameIDs))
	ids := make([]int64, 0, len(galgameIDs))
	for _, id := range galgameIDs {
		if id <= 0 {
			continue
		}
		v := int64(id)
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		ids = append(ids, v)
	}
	var workIDs []int64
	for start := 0; start < len(ids); start += mapChunk {
		end := min(start+mapChunk, len(ids))
		var page []int64
		if err := t.db.WithContext(ctx).Raw(
			`SELECT id FROM catalog_work
			 WHERE site = ? AND product_work_id IN (?) AND deleted_at IS NULL`,
			siteGalgameWiki, ids[start:end]).Scan(&page).Error; err != nil {
			return nil, fmt.Errorf("map galgame ids to claimed works: %w", err)
		}
		workIDs = append(workIDs, page...)
	}
	return workIDs, nil
}
