package intromt

import (
	"context"
	"fmt"

	"api/internal/jobs/workpop"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Population selects the candidate pool.
type Population string

const (
	// PopulationBodyless — catalog-native works (empty site): the doc-75 pilot
	// lane (dlsite/bangumi-anchored works whose ja intro came from an importer).
	PopulationBodyless Population = "bodyless"
	// PopulationClaimed — current product works (site='kungal'): the retirement
	// refill lane. Their source ja intros are already materialized in
	// catalog_work_intro while the former wiki zh translations were deliberately
	// dropped as unsourced (ruling ①, refs/plans/10), so this lane refills
	// zh-Hans from the ja original. Works with no ja row are not candidates.
	PopulationClaimed Population = "claimed"

	// PopulationPublished is the claimed population narrowed to what is
	// actually on the public face. Claimed is 64,530 works in prod of which
	// only 10,970 are published; the rest are the draft sea this track has
	// repeatedly declined to spend translation budget on (the step-75 ruling).
	// The predicate mirrors model.ClaimStateKey's `live` rule exactly — NULL
	// claim_state is live, and a row without product_work_id reads as unclaimed
	// on the wire so it must filter as unclaimed here.
	PopulationPublished Population = "published"
)

// SourceLang selects which language the machine lane translates FROM.
type SourceLang string

const (
	// SourceJa is the original lane (doc 75): the upstream's own Japanese blurb.
	SourceJa SourceLang = "ja"

	// SourceEn translates the English text instead, for works that have no
	// Japanese anywhere. It is a LAST RESORT and is kept strictly disjoint from
	// SourceJa: a work with any ja intro is excluded, because ja→zh is a
	// shorter, more faithful hop than en→zh (the English is itself usually a
	// translation, so en→zh is a relay and compounds both hops' losses).
	SourceEn SourceLang = "en"
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

// candidate is one galgame work eligible for the ja→zh-Hans MT job.
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
//	catalog_work (population lane: bodyless or claimed)
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
//
// The claimed lane reuses the same rank unchanged: claimed works typically
// carry no dlsite popularity, so they rank 0 and fall back to the work_id ASC
// tiebreak — still a total, deterministic order. Deliberately no join onto the
// retired galgame table for view counts: the whole lane is translated in one
// run anyway, so order is cosmetic.
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, pop Population, src SourceLang, top, limit int) ([]candidate, error) {
	if src == "" {
		src = SourceJa
	}
	if src != SourceJa && src != SourceEn {
		return nil, fmt.Errorf("unknown source language %q (want %q or %q)", src, SourceJa, SourceEn)
	}
	// The English lane is a LAST RESORT, so it excludes two populations the
	// Japanese path serves better:
	//
	//   - anything with a ja intro, which the ja lane already translates in a
	//     single hop (en→zh would be a relay through a translation);
	//   - anything Getchu anchors, because that crawler is about to supply the
	//     Japanese original (refs/proj/167). Writing an en→zh row now would
	//     LOCK IT IN: fill-missing means the later, better ja→zh translation
	//     would find zh already present and skip.
	//
	// Both are exclusions rather than orderings on purpose — a lane that "runs
	// second" is not a guarantee, a NOT EXISTS is.
	lastResortGate := ""
	if src == SourceEn {
		lastResortGate = `
		  AND NOT EXISTS (SELECT 1 FROM catalog_work_intro j WHERE j.work_id = b.id AND j.lang = 'ja')
		  AND NOT EXISTS (
			SELECT 1 FROM catalog_release rel
			JOIN catalog_external_ref g ON g.entity_type = 6 AND g.entity_id = rel.id
				AND g.source_id = (SELECT id FROM catalog_source WHERE key = 'getchu')
			WHERE rel.work_id = b.id AND rel.deleted_at IS NULL)`
	}
	if top <= 0 {
		top = 5000
	}
	sitePredicate, err := sitePredicateFor(pop)
	if err != nil {
		return nil, err
	}
	q := db.WithContext(ctx).Raw(`
		WITH pool AS (
			SELECT id FROM catalog_work
			WHERE medium_id = ? AND `+sitePredicate+` AND deleted_at IS NULL
		),
		has_zh_source AS (
			SELECT DISTINCT work_id FROM catalog_work_intro
			WHERE lang IN ('zh-Hans','zh-Hant') AND provenance = 0
		),
		ja AS (
			SELECT DISTINCT ON (work_id) work_id, source_id AS ja_source_id, intro AS ja_text
			FROM catalog_work_intro WHERE lang = ?
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
		FROM pool b
		JOIN ja ON ja.work_id = b.id
		LEFT JOIN has_zh_source hs ON hs.work_id = b.id
		LEFT JOIN mzh ON mzh.work_id = b.id
		LEFT JOIN pop ON pop.work_id = b.id
		WHERE hs.work_id IS NULL`+lastResortGate+`
		ORDER BY COALESCE(pop.dl, pop.wl, 0) DESC, b.id ASC
		LIMIT ?`,
		reg.galgameMedium, string(src),
		reg.dlsiteSource, model.PopularityMetricDownloads,
		reg.dlsiteSource, model.PopularityMetricWishlist,
		top)
	var out []candidate
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	// Strip upstream markup BEFORE anything hashes or translates this text
	// (sanitize.go): the model carries VNDB's link syntax through verbatim, and
	// hashing the cleaned text is what makes already-dirty rows re-translate.
	for i := range out {
		out[i].JaText = sanitizeSource(out[i].JaText)
	}
	// Window in Go AFTER the popularity ORDER BY so a sample run (--limit)
	// takes the most-popular N (the strongest quality signal), not an arbitrary
	// slice.
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// sitePredicateFor renders a population as a catalog_work predicate, over
// unqualified columns (this job's candidate query has no table alias).
//
// The definitions live in workpop so the four lanes that need them cannot
// drift from each other or from model.ClaimStateKey. This job's own vocabulary
// stays narrower on purpose: unlike the enrichment lanes it has no "all", and
// an empty population is an error rather than "everything" — a translation run
// is expensive, and defaulting it to the whole table is not a safe default.
//
// "claimed" is a PROPERTY — has a site — and never the literal site value.
// Wave 161 renamed the only value that has ever existed (galgame_wiki →
// kungal), and every lane that had pinned the literal went silently empty for
// three days while reporting a clean zero-candidate run.
func sitePredicateFor(pop Population) (string, error) {
	switch pop {
	case PopulationBodyless, PopulationClaimed, PopulationPublished:
		return workpop.Predicate(workpop.Population(pop), "")
	default:
		return "", fmt.Errorf("unknown population %q (want %q, %q or %q)",
			pop, PopulationBodyless, PopulationClaimed, PopulationPublished)
	}
}
