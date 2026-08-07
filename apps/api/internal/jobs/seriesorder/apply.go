package seriesorder

import (
	"context"

	"gorm.io/gorm"
)

// Current is a member row's stored facets, keyed by work id.
type Current struct {
	Position int16
	Kind     int16
}

// LoadCurrent reads the stored facets of one series' member rows.
func LoadCurrent(ctx context.Context, db *gorm.DB, seriesID int64) (map[int64]Current, error) {
	var rows []struct {
		WorkID   int64 `gorm:"column:work_id"`
		Position int16 `gorm:"column:position"`
		Kind     int16 `gorm:"column:kind"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT work_id, position, kind FROM catalog_series_member WHERE series_id = ?`,
		seriesID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]Current, len(rows))
	for _, r := range rows {
		out[r.WorkID] = Current{Position: r.Position, Kind: r.Kind}
	}
	return out, nil
}

// Apply reconciles one series' member facets against the computed assignments,
// writing ONLY the rows that really moved and returning their work ids so the
// caller can touch them. A member absent from `have` is skipped rather than
// inserted: membership is the lane job's business, this package only orders it.
//
// In a dry run nothing is written and the same ids come back, so the forecast
// and the write agree by construction — and a second pass over unchanged data
// returns an empty slice, which is the idempotency proof both jobs assert.
func Apply(ctx context.Context, db *gorm.DB, seriesID int64, want []Assignment,
	have map[int64]Current, apply bool) ([]int64, error) {
	var changed []int64
	for _, a := range want {
		cur, ok := have[a.WorkID]
		if !ok {
			continue
		}
		if cur.Position == a.Position && cur.Kind == a.Kind {
			continue
		}
		changed = append(changed, a.WorkID)
		if !apply {
			continue
		}
		if err := db.WithContext(ctx).Exec(`
			UPDATE catalog_series_member SET position = ?, kind = ?, updated_at = now()
			WHERE series_id = ? AND work_id = ?`, a.Position, a.Kind, seriesID, a.WorkID).Error; err != nil {
			return nil, err
		}
	}
	return changed, nil
}
