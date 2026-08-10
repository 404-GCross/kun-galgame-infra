package bgmsummaries

import (
	"context"
	"fmt"

	"api/internal/jobs/workpop"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type registry struct {
	galgameMedium int16
	bangumiSource int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if r.galgameMedium == 0 || r.bangumiSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, bangumi source=%d)", r.galgameMedium, r.bangumiSource)
	}
	return r, nil
}

const (
	PopulationAll       = "all"
	PopulationBodyless  = "bodyless"
	PopulationClaimed   = "claimed"
	PopulationPublished = "published"
)

// sitePredicate renders the population as a catalog_work SQL predicate.
//
// Claimed is "has a site", not "site = <some product>" — wave 161 renamed the
// only value there has ever been (galgame_wiki → kungal) and pinning a literal
// is what silently emptied four other lanes.
//
// PUBLISHED is the narrower, product-facing population: claimed AND actually on
// the public face. It is NOT a synonym for claimed — in prod, claimed is 64,530
// works of which only 10,970 are published; the rest are the draft sea, which
// this track has repeatedly declined to invest translation budget in (the
// step-75 ruling). The predicate mirrors model.ClaimStateKey's `live` rule
// EXACTLY, including its two easily-missed halves: a NULL claim_state is live
// (a claimed row no projector has stamped yet), and a row without
// product_work_id reads as unclaimed on the wire and must filter as unclaimed
// here. Those two must not drift — a lane that selected a different set than
// the read face renders is the bug ClaimStateKey exists to prevent.
// The definitions themselves live in workpop, so the lanes that share them
// cannot drift from each other or from model.ClaimStateKey.
func sitePredicate(pop string) (string, error) {
	return workpop.Predicate(workpop.Population(pop), "w")
}

type candidate struct {
	WorkID    int64   `gorm:"column:work_id"`
	SubjectID int64   `gorm:"column:subject_id"`
	Site      *string `gorm:"column:site"`
	Summary   string  `gorm:"column:summary"`
}

func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, pop string, limit, offset int) ([]candidate, error) {
	site, err := sitePredicate(pop)
	if err != nil {
		return nil, err
	}
	q := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				w.site AS site, sub.summary AS summary
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.medium_id = ? AND `+site+` AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact, reg.galgameMedium)
	var out []candidate
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	if offset > 0 {
		if offset >= len(out) {
			return nil, nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func preloadExistingLangs(ctx context.Context, db *gorm.DB, workIDs []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT work_id, lang FROM catalog_work_intro WHERE work_id IN ?`, workIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		set := out[r.WorkID]
		if set == nil {
			set = map[string]bool{}
			out[r.WorkID] = set
		}
		set[r.Lang] = true
	}
	return out, nil
}
