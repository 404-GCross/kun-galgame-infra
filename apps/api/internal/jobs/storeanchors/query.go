package storeanchors

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type candidate struct {
	ReleaseID int64  `gorm:"column:release_id"`
	WorkID    int64  `gorm:"column:work_id"`
	RawValue  string `gorm:"column:raw_value"`
}

func loadCandidates(ctx context.Context, db *gorm.DB, vndbSource, laneSource int16, site string, limit int) ([]candidate, error) {
	q := `
		SELECT rel.id AS release_id, rel.work_id, e.value AS raw_value
		FROM catalog_release rel
		JOIN catalog_external_ref vr ON vr.entity_type = ? AND vr.entity_id = rel.id
			AND vr.source_id = ? AND vr.link_kind = ?
		JOIN src_vndb.releases_extlinks rx ON rx.id = vr.external_id
		JOIN src_vndb.extlinks e ON e.id = rx.link AND e.site = ?
		WHERE rel.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM catalog_external_ref x
			WHERE x.entity_type = ? AND x.entity_id = rel.id AND x.source_id = ?)
		ORDER BY rel.id, e.value`
	args := []any{
		model.EntityTypeRelease, vndbSource, model.LinkKindExact, site,
		model.EntityTypeRelease, laneSource,
	}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	var out []candidate
	if err := db.WithContext(ctx).Raw(q, args...).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func loadTakenExact(ctx context.Context, db *gorm.DB, laneSource int16) (map[string]struct{}, error) {
	var vals []string
	if err := db.WithContext(ctx).Raw(`
		SELECT external_id FROM catalog_external_ref
		WHERE entity_type = ? AND source_id = ? AND link_kind = ?`,
		model.EntityTypeRelease, laneSource, model.LinkKindExact).Scan(&vals).Error; err != nil {
		return nil, fmt.Errorf("load exact-held ids: %w", err)
	}
	out := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		out[v] = struct{}{}
	}
	return out, nil
}

func loadRejections(ctx context.Context, db *gorm.DB, laneSource int16) (map[string]struct{}, error) {
	var rows []struct {
		EntityID   int64  `gorm:"column:entity_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT entity_id, external_id FROM catalog_match_rejection
		WHERE entity_type = ? AND source_id = ?`,
		model.EntityTypeRelease, laneSource).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load rejections: %w", err)
	}
	out := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		out[rejKey(r.EntityID, r.ExternalID)] = struct{}{}
	}
	return out, nil
}

func rejKey(releaseID int64, externalID string) string {
	return fmt.Sprintf("%d\x00%s", releaseID, externalID)
}

func resolveSource(ctx context.Context, db *gorm.DB, key string) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("resolve source %q: %w", key, err)
	}
	if id == 0 {
		return 0, fmt.Errorf("catalog_source has no %q row — run the seed first", key)
	}
	return id, nil
}
