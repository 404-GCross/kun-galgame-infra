package repository

import (
	"context"

	"gorm.io/gorm"
)

const touchChunk = 2000

func TouchWorks(ctx context.Context, db *gorm.DB, workIDs []int64) error {
	if len(workIDs) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(workIDs))
	ids := make([]int64, 0, len(workIDs))
	for _, id := range workIDs {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for start := 0; start < len(ids); start += touchChunk {
		end := min(start+touchChunk, len(ids))
		if err := db.WithContext(ctx).
			Exec(`UPDATE catalog_work SET updated_at = now() WHERE id IN (?)`, ids[start:end]).
			Error; err != nil {
			return err
		}
	}
	return nil
}
