package intromt

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the ids this job resolves by key (never hardcoded) so a
// rehearsal / prod DB with different auto-increment seeds still works.
type registry struct {
	galgameMedium int16
	dlsiteSource  int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&r.dlsiteSource).Error; err != nil {
		return r, fmt.Errorf("resolve dlsite source: %w", err)
	}
	if r.galgameMedium == 0 || r.dlsiteSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, dlsite source=%d)", r.galgameMedium, r.dlsiteSource)
	}
	return r, nil
}

// candidate is one bodyless galgame work eligible for the ja→zh-Hans MT pilot.
//
//   - JaSourceID / JaText: the CHOSEN ja intro — the lowest-source_id ja row,
//     i.e. the one the read face surfaces. The machine row is attributed to
//     that source_id and its src_hash is sha256(JaText).
//   - MZhID / MZhSrcHash: the work's EXISTING machine zh-Hans row, if any (a
//     prior pilot run). Present → idempotence / re-translate decision; absent →
//     a fresh insert. NULL MZhID means no machine row yet.
//   - PopScore: the pinned popularity rank key (see loadCandidates).
//
// Works carrying ANY zh-Hans/zh-Hant SOURCE row (provenance=0) are excluded at
// the query layer — the fill-missing-language rule: MT never competes with
// human/source zh text.
type candidate struct {
	WorkID     int64   `gorm:"column:work_id"`
	JaSourceID int16   `gorm:"column:ja_source_id"`
	JaText     string  `gorm:"column:ja_text"`
	MZhID      *int64  `gorm:"column:mzh_id"`
	MZhSrcHash *string `gorm:"column:mzh_src_hash"`
	PopScore   int64   `gorm:"column:pop_score"`
}

// loadCandidates resolves the popularity-ranked candidate set:
//
//	catalog_work (bodyless galgame)
//	  → has a lang='ja' intro row (the chosen row = lowest source_id)
//	  → has NO lang IN ('zh-Hans','zh-Hant') row with provenance=0 (fill-missing)
//	  LEFT JOIN its existing machine zh-Hans row (provenance=1), if any
//	  LEFT JOIN popularity
//
// PINNED popularity ordering (doc 75, "downloads 优先,缺则 wishlist"): the rank
// key is COALESCE(dlsite downloads, dlsite wishlist, 0) — downloads is the
// preferred signal, wishlist the per-work fallback, 0 when neither exists.
// Ordered DESC with a work_id ASC final tiebreak → a TOTAL, deterministic order
// so `top` selects the same set every run. `top` caps the pilot population
// (5,000); Go-side windowing by `limit` then takes the most-popular N for a
// sample run.
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, top, limit int) ([]candidate, error) {
	if top <= 0 {
		top = 5000
	}
	q := db.WithContext(ctx).Raw(`
		WITH bodyless AS (
			SELECT id FROM catalog_work
			WHERE medium_id = ? AND (site IS NULL OR site = '') AND deleted_at IS NULL
		),
		has_zh_source AS (
			SELECT DISTINCT work_id FROM catalog_work_intro
			WHERE lang IN ('zh-Hans','zh-Hant') AND provenance = 0
		),
		ja AS (
			SELECT DISTINCT ON (work_id) work_id, source_id AS ja_source_id, intro AS ja_text
			FROM catalog_work_intro WHERE lang = 'ja'
			ORDER BY work_id, source_id
		),
		mzh AS (
			SELECT DISTINCT ON (work_id) work_id, id AS mzh_id, src_hash AS mzh_src_hash
			FROM catalog_work_intro WHERE lang = 'zh-Hans' AND provenance = 1
			ORDER BY work_id, source_id
		),
		pop AS (
			SELECT work_id,
				max(value) FILTER (WHERE source_id = ? AND metric = ?) AS dl,
				max(value) FILTER (WHERE source_id = ? AND metric = ?) AS wl
			FROM catalog_work_popularity GROUP BY work_id
		)
		SELECT b.id AS work_id, ja.ja_source_id, ja.ja_text,
			mzh.mzh_id, mzh.mzh_src_hash,
			COALESCE(pop.dl, pop.wl, 0) AS pop_score
		FROM bodyless b
		JOIN ja ON ja.work_id = b.id
		LEFT JOIN has_zh_source hs ON hs.work_id = b.id
		LEFT JOIN mzh ON mzh.work_id = b.id
		LEFT JOIN pop ON pop.work_id = b.id
		WHERE hs.work_id IS NULL
		ORDER BY COALESCE(pop.dl, pop.wl, 0) DESC, b.id ASC
		LIMIT ?`,
		reg.galgameMedium,
		reg.dlsiteSource, model.PopularityMetricDownloads,
		reg.dlsiteSource, model.PopularityMetricWishlist,
		top)
	var out []candidate
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	// Window in Go AFTER the popularity ORDER BY so a sample run (--limit)
	// takes the most-popular N (the strongest quality signal), not an arbitrary
	// slice.
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}
