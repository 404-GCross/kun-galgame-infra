package bgmzhnames

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func personLane() laneSpec {
	return laneSpec{
		load: loadAnchoredPersons,
		preload: func(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]*zhAliasState, error) {
			return preloadZhAliasesBy(ctx, db, "catalog_name_alias", "credit_name_id", ids)
		},
		insert:        insertNameAlias,
		dropOwnerName: true,
	}
}

func loadAnchoredPersons(ctx context.Context, db *gorm.DB, sourceID int16, limit, offset int) ([]anchoredEntity, error) {
	const query = `SELECT DISTINCT ON (p.id) p.id AS entity_id,
			p.primary_credit_name_id AS owner_id, COALESCE(cn.name, '') AS owner_name,
			r.external_id, sb.infobox_parsed
		FROM catalog_person p
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = p.id
			AND r.source_id = ? AND r.link_kind = ?
		JOIN src_bangumi.person sb ON sb.id = r.external_id::bigint
		LEFT JOIN catalog_credit_name cn ON cn.id = p.primary_credit_name_id
		WHERE p.deleted_at IS NULL
		ORDER BY p.id, r.external_id::bigint`
	var out []anchoredEntity
	if err := db.WithContext(ctx).
		Raw(query, model.EntityTypePerson, sourceID, model.LinkKindExact).Scan(&out).Error; err != nil {
		return nil, fmt.Errorf("load anchored persons: %w", err)
	}
	return window(out, limit, offset), nil
}

func insertNameAlias(ctx context.Context, db *gorm.DB, creditNameID int64, name string, primary bool) (bool, error) {
	res := db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "credit_name_id"}, {Name: "name"}, {Name: "lang"}},
		DoNothing: true,
	}).Create(&model.CatalogNameAlias{
		CreditNameID:       creditNameID,
		Name:               name,
		Lang:               LangZhHans,
		Kind:               model.AliasKindTranslation,
		IsPrimaryForLocale: primary,
		SourceID:           bangumiSourceRef(),
		Provenance:         model.AliasProvenanceSource,
	})
	return res.RowsAffected > 0, res.Error
}
