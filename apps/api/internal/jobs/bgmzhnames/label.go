package bgmzhnames

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func labelLane() laneSpec {
	return laneSpec{
		load: loadAnchoredLabels,
		preload: func(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]*zhAliasState, error) {
			return preloadZhAliasesBy(ctx, db, "catalog_label_alias", "label_id", ids)
		},
		insert:        insertLabelAlias,
		dropOwnerName: true,
	}
}

func loadAnchoredLabels(ctx context.Context, db *gorm.DB, sourceID int16, limit, offset int) ([]anchoredEntity, error) {
	const query = `SELECT DISTINCT ON (l.id) l.id AS entity_id, l.id AS owner_id,
			l.display_name AS owner_name, r.external_id, sb.infobox_parsed
		FROM catalog_label l
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = l.id
			AND r.source_id = ? AND r.link_kind = ?
		JOIN src_bangumi.person sb ON sb.id = r.external_id::bigint
		WHERE l.deleted_at IS NULL
		ORDER BY l.id, r.external_id::bigint`
	var out []anchoredEntity
	if err := db.WithContext(ctx).
		Raw(query, model.EntityTypeLabel, sourceID, model.LinkKindExact).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load anchored labels: %w", err)
	}
	return window(out, limit, offset), nil
}

func insertLabelAlias(ctx context.Context, db *gorm.DB, labelID int64, name string, primary bool) (bool, error) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "label_id"}, {Name: "name"}, {Name: "lang"}},
		DoNothing: true,
	}).Create(&model.CatalogLabelAlias{
		LabelID:            labelID,
		Name:               name,
		Lang:               LangZhHans,
		Kind:               model.AliasKindTranslation,
		IsPrimaryForLocale: primary,
		SourceID:           bangumiSourceRef(),
		Provenance:         model.AliasProvenanceSource,
	})
	return res.RowsAffected > 0, res.Error
}
