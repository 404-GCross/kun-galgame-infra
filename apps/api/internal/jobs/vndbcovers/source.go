package vndbcovers

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the two catalog registry ids this backfill needs, resolved by
// key (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works — the same discipline the dlsite / bangumi / getchu lanes
// follow.
type registry struct {
	vndbSource    int16
	galgameMedium int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&r.vndbSource).Error; err != nil {
		return r, fmt.Errorf("resolve vndb source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if r.vndbSource == 0 || r.galgameMedium == 0 {
		return r, fmt.Errorf("registry not seeded (vndb source=%d, galgame medium=%d)", r.vndbSource, r.galgameMedium)
	}
	return r, nil
}

// candidate is one galgame work with no cover, joined to the vn it is exactly
// anchored to. VNDBID carries the anchor VERBATIM ("v38") — that is both what
// catalog_external_ref stores and what the API's id filter expects, so nothing
// is re-formatted in between.
type candidate struct {
	WorkID int64  `gorm:"column:work_id"`
	VNDBID string `gorm:"column:vndb_id"`
}

// loadCandidates resolves the galgame works reachable via an EXACT VNDB work
// anchor that show NO cover at all today:
//
//	catalog_work(galgame, alive)
//	  → catalog_external_ref(entity_type=work, source=vndb, link_kind=exact)
//	  ∧ NOT EXISTS catalog_work_cover
//
// "Shows no cover" is the whole admission rule and it is also the idempotency
// gate: a work that gained a cover between runs is not a candidate at all, so
// it is skipped before any VNDB call. VNDB is a FALLBACK here, never a
// supplement — a work with a wiki / DLsite / Getchu cover keeps exactly what it
// has.
//
// Only EXACT anchors qualify; probable/related links are a guess and a wrong
// cover is a worse failure than no cover. DISTINCT ON keeps ONE vn per work in
// the theoretical case a work carries several exact anchors (the anti-squatting
// unique index makes that near-impossible), so the row count is the work count.
//
// ids, when non-empty, restricts the sweep to an explicit work list; the anchor
// and no-cover predicates still apply, so naming a work that already has a
// cover forecasts nothing rather than adding a second one.
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, ids []int64) ([]candidate, error) {
	sql := `
		SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS vndb_id
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
			AND r.source_id = ? AND r.link_kind = ?
		WHERE w.medium_id = ? AND w.deleted_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM catalog_work_cover c WHERE c.work_id = w.id)`
	args := []any{model.EntityTypeWork, reg.vndbSource, model.LinkKindExact, reg.galgameMedium}
	if len(ids) > 0 {
		sql += "\n\t\t\tAND w.id IN ?"
		args = append(args, ids)
	}
	sql += "\n\t\tORDER BY w.id, r.external_id"

	var out []candidate
	if err := db.WithContext(ctx).Raw(sql, args...).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
