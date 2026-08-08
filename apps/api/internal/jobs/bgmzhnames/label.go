package bgmzhnames

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// labelLane projects the Chinese brand / company names Bangumi carries for a
// 会社 or 同人サークル into catalog_label_alias (refs/proj/175 wave A).
//
// The staging join is src_bangumi.person, NOT a table of its own: Bangumi files
// companies (type 2) and groups (type 3) in the same `person` table as
// individuals, with the same infobox shape, so the whole parse layer is shared
// verbatim. The lane does NOT filter on that type — the EXACT anchor already
// decided this row is this label, and a type filter could only ever disagree
// with the anchor.
//
// NO TOUCH DISCIPLINE (hostWorks stays nil), which is a deliberate carry-over,
// not an omission: labellogos (wave 170) writes catalog_label.logo_hash — a
// label field that every work brief renders — and touches no work either. A
// label's alias set reaches its own read face (PublicService.labelAliases) and
// the work read faces render a label's display_name, which this lane never
// changes. Adding a touch here would bump ~2.7k labels' whole back-catalogue
// through the public changes feed for a field those works do not show.
func labelLane() laneSpec {
	return laneSpec{
		load: loadAnchoredLabels,
		preload: func(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]*zhAliasState, error) {
			return preloadZhAliasesBy(ctx, db, "catalog_label_alias", "label_id", ids)
		},
		insert: insertLabelAlias,
		// The public face already hides an alias equal to the display name
		// (`a.name <> l.display_name`), so writing one would only cost a dead
		// row — and a dead row that claims the locale primary. 72 of the
		// anchored labels are in exactly that state: their display_name IS the
		// Chinese name already.
		dropOwnerName: true,
	}
}

// loadAnchoredLabels resolves every live label carrying an EXACT bangumi
// anchor, with its parsed infobox. DISTINCT ON keeps ONE anchor per label
// (lowest external id, numerically), so a label with two anchors is projected
// once and deterministically.
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
