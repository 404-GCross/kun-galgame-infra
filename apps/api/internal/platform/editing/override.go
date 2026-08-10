package editing

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
)

const overrideChunk = 2000

func EditedEntities(ctx context.Context, db *gorm.DB, entityType, fieldKey string, entityIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool)
	if len(entityIDs) == 0 {
		return out, nil
	}
	needle, err := json.Marshal([]string{fieldKey})
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(entityIDs); start += overrideChunk {
		end := min(start+overrideChunk, len(entityIDs))
		var ids []int64
		if err := db.WithContext(ctx).Raw(`
			SELECT DISTINCT entity_id FROM edit_revision
			WHERE entity_type = ? AND entity_id IN (?) AND changed_fields @> ?::jsonb`,
			entityType, entityIDs[start:end], string(needle)).Scan(&ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, nil
}
