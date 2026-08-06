package bgmzhnames

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// personLane projects the Chinese names Bangumi carries for an individual into
// catalog_name_alias (refs/proj/175 wave A).
//
// THE OWNER IS A NAME, NOT THE PERSON. catalog_name_alias hangs off
// catalog_credit_name — the doc 10 invariant that identity lives in credit
// names — so the row is attached to the person's PRIMARY credit name, the one
// the read face displays. A person with no primary credit name yet is COUNTED
// (Stats.SkippedNoOwner) and skipped: picking one of its other names would be
// exactly the identity judgement this lane must not make.
//
// The anchor read is the PERSON-level one (entity_type=0) and never a
// credit-name anchor — projecting a NAME's source page onto its person would
// smuggle in the deferred identity resolution (the CatalogPersonIntro ruling).
//
// NO TOUCH DISCIPLINE (hostWorks stays nil): a person's names do not reach any
// work read face, which is the same finding personmint recorded ("no
// TouchWorks: person does not reach any work read face").
func personLane() laneSpec {
	return laneSpec{
		load: loadAnchoredPersons,
		preload: func(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]*zhAliasState, error) {
			return preloadZhAliasesBy(ctx, db, "catalog_name_alias", "credit_name_id", ids)
		},
		insert: insertNameAlias,
		// An alias identical to the credit name it hangs off is a variant of
		// nothing; 213 anchored persons are already spelled in Chinese.
		dropOwnerName: true,
	}
}

// loadAnchoredPersons resolves every live person carrying an EXACT bangumi
// anchor, with its parsed infobox and its primary credit name. The LEFT JOIN
// keeps persons with no primary credit name in the universe so the run can
// report them instead of silently narrowing itself.
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
	})
	return res.RowsAffected > 0, res.Error
}
