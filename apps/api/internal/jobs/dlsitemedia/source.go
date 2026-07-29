package dlsitemedia

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the two catalog registry ids this backfill needs, resolved by
// key (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works. In practice both are stable (galgame medium=1, dlsite
// source=4) but resolving keeps the tool honest.
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

// siteGalgameWiki is the claim marker on catalog_work.site: the work has a
// galgame (wiki) body, keyed by product_work_id.
const siteGalgameWiki = "galgame_wiki"

// candidate is one galgame work joined to its DLsite workno anchor. Site is
// carried (not filtered out) so the write-time guard can decide PER KIND which
// lane the work belongs to: intro/cover stay bodyless-only (whole-facet XOR),
// screenshot admits the claimed lane too (refs/proj/125).
type candidate struct {
	WorkID int64   `gorm:"column:work_id"`
	Workno string  `gorm:"column:workno"`
	Site   *string `gorm:"column:site"`
}

// loadCandidates resolves the works this run may write, as the concatenation of
// the two lanes (bodyless first, claimed appended). The claimed lane only exists
// when the run backfills screenshots — it is a screenshot-facet decision
// (refs/proj/125), so an intro/cover-only run resolves exactly the pre-125
// candidate set.
//
// WINDOWING IS ASYMMETRIC, and deliberately so. The two lanes differ in a way
// that matters for chunked --apply campaigns:
//
//   - the BODYLESS lane is STABLE across runs (its query has no "already done"
//     predicate), so --offset is meaningful there and keeps its pre-125 meaning.
//   - the CLAIMED lane CONSUMES ITSELF: every work an --apply chunk fills gains a
//     catalog_work_screenshot row and drops out of the next run's lane. Offsetting
//     INTO a shrinking list skips works — chunk k starts where chunk k-1's writes
//     already moved the head, leaving alternating gaps unwritten. So --offset
//     never enters the claimed lane; it is always taken from its own head, which
//     is also how it self-resumes after a quota abort.
//
// Hence: --offset windows the bodyless lane only, --limit caps the concatenation.
// `--offset <bodyless-count>` still isolates the claimed lane for a dry run, and
// stays correct under --apply because the offset only ever consumes stable rows.
// Windowing happens in Go after the per-lane DISTINCT ON so it applies to distinct
// works (DISTINCT ON + LIMIT in one query can only window on the DISTINCT order
// key, which is exactly w.id here).
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, kinds Kinds, limit, offset int) ([]candidate, error) {
	out, err := loadBodylessCandidates(ctx, db, reg)
	if err != nil {
		return nil, fmt.Errorf("bodyless lane: %w", err)
	}
	if offset > 0 {
		if offset >= len(out) {
			out = nil
		} else {
			out = out[offset:]
		}
	}
	if kinds.Screenshot {
		claimed, err := loadClaimedScreenshotCandidates(ctx, db, reg)
		if err != nil {
			return nil, fmt.Errorf("claimed screenshot lane: %w", err)
		}
		out = append(out, claimed...)
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

// loadBodylessCandidates resolves bodyless galgame works reachable via an EXACT
// DLsite workno release anchor:
//
//	catalog_work(bodyless galgame) → catalog_release(work_id)
//	  → catalog_external_ref(entity_type=release, source_id=dlsite, link_kind=exact, external_id=workno)
//
// DISTINCT ON keeps ONE workno per work (the lowest) — a work with several DLsite
// releases sources its media from one anchor.
func loadBodylessCandidates(ctx context.Context, db *gorm.DB, reg registry) ([]candidate, error) {
	var out []candidate
	err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno, w.site AS site
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&out).Error
	return out, err
}

// loadClaimedScreenshotCandidates resolves the CLAIMED half of the screenshot
// lane (refs/proj/125): a claimed galgame work whose read face shows NO
// screenshot at all today, reachable via the same EXACT DLsite workno release
// anchor. Three conditions, all load-bearing:
//
//   - site='galgame_wiki' — the claimed population, admitted by the (facet,
//     source) XOR: dlsite is a catalog-native source, so its rows live in
//     catalog_work_screenshot for claimed and bodyless alike.
//   - no bridged screenshot (galgame_screenshot on the work's galgame body) — the
//     targeting rule. A work whose wiki body already has screenshots would show
//     both lanes at once; DLsite store samples are a FALLBACK for the works that
//     have nothing, never a supplement to real wiki screenshots.
//   - no native screenshot — idempotency at the candidate level (preloadExisting +
//     ON CONFLICT still guard the write itself).
//
// A claimed work with a NULL product_work_id has no bridge to collide with, so
// the galgame_screenshot NOT EXISTS admits it, which is the intended reading.
func loadClaimedScreenshotCandidates(ctx context.Context, db *gorm.DB, reg registry) ([]candidate, error) {
	var out []candidate
	err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno, w.site AS site
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND w.site = ? AND w.deleted_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM galgame_screenshot gs WHERE gs.galgame_id = w.product_work_id)
				AND NOT EXISTS (SELECT 1 FROM catalog_work_screenshot cs WHERE cs.work_id = w.id)
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium, siteGalgameWiki).
		Scan(&out).Error
	return out, err
}

// dlsiteMeta is the per-work media metadata derived from the dlsite staging DB.
// A zero value (workno absent from staging) is a valid "nothing to write".
type dlsiteMeta struct {
	Age         string   // age_category ('1' general / '2' r15 / '3' adult)
	Intro       string   // rebuilt from page_json.parts (may be "")
	CoverFile   string   // image_main mirror filename ("" = placeholder/absent)
	SampleFiles []string // image_samples mirror filenames, ordered (index = sort_order)
}

type dlsiteRow struct {
	Workno      string `gorm:"column:workno"`
	AgeCategory string `gorm:"column:age_category"`
	ProductJSON []byte `gorm:"column:product_json"`
	PageJSON    []byte `gorm:"column:page_json"`
}

// loadDlsiteMeta reads the media JSON for a batch of worknos from the dlsite
// staging DB and derives the per-work metadata. It reads staging JSON only; the
// image BYTES come from the local mirror (never DLsite).
func loadDlsiteMeta(ctx context.Context, dldb *gorm.DB, worknos []string) (map[string]dlsiteMeta, error) {
	var rows []dlsiteRow
	if err := dldb.WithContext(ctx).
		Raw(`SELECT workno, age_category, product_json, page_json FROM works WHERE workno IN ?`, worknos).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]dlsiteMeta, len(rows))
	for _, r := range rows {
		cover, _ := coverFile(r.ProductJSON)
		out[r.Workno] = dlsiteMeta{
			Age:         r.AgeCategory,
			Intro:       introFromPage(r.PageJSON),
			CoverFile:   cover,
			SampleFiles: sampleFiles(r.ProductJSON),
		}
	}
	return out, nil
}

// existing holds the already-present dlsite-sourced rows for the candidate set,
// so a re-run skips BEFORE any byte read/upload (the charportraits idempotency
// discipline). ON CONFLICT DO NOTHING is the ultimate guard on the write; this
// preload just avoids re-reading files and re-hitting the image service.
type existing struct {
	intro map[int64]bool         // work has a (lang='ja', dlsite) intro row
	cover map[int64]bool         // work has any dlsite cover row
	shot  map[int64]map[int]bool // work -> set of dlsite screenshot sort_orders present
}

// preloadExisting loads the existing dlsite-sourced media markers for the given
// works in three queries (one per table).
func preloadExisting(ctx context.Context, db *gorm.DB, workIDs []int64, sourceID int16, lang string) (*existing, error) {
	e := &existing{intro: map[int64]bool{}, cover: map[int64]bool{}, shot: map[int64]map[int]bool{}}
	if len(workIDs) == 0 {
		return e, nil
	}
	db = db.WithContext(ctx)

	var introWorks []int64
	if err := db.Raw(`SELECT work_id FROM catalog_work_intro WHERE source_id = ? AND lang = ? AND work_id IN ?`,
		sourceID, lang, workIDs).Scan(&introWorks).Error; err != nil {
		return nil, fmt.Errorf("preload intros: %w", err)
	}
	for _, id := range introWorks {
		e.intro[id] = true
	}

	var coverWorks []int64
	if err := db.Raw(`SELECT DISTINCT work_id FROM catalog_work_cover WHERE source_id = ? AND work_id IN ?`,
		sourceID, workIDs).Scan(&coverWorks).Error; err != nil {
		return nil, fmt.Errorf("preload covers: %w", err)
	}
	for _, id := range coverWorks {
		e.cover[id] = true
	}

	type shotRow struct {
		WorkID    int64 `gorm:"column:work_id"`
		SortOrder int   `gorm:"column:sort_order"`
	}
	var shots []shotRow
	if err := db.Raw(`SELECT work_id, sort_order FROM catalog_work_screenshot WHERE source_id = ? AND work_id IN ?`,
		sourceID, workIDs).Scan(&shots).Error; err != nil {
		return nil, fmt.Errorf("preload screenshots: %w", err)
	}
	for _, s := range shots {
		set := e.shot[s.WorkID]
		if set == nil {
			set = map[int]bool{}
			e.shot[s.WorkID] = set
		}
		set[s.SortOrder] = true
	}
	return e, nil
}
