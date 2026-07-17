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

// candidate is one bodyless galgame work joined to its DLsite workno anchor.
// Site is carried (not filtered out) so the write-time XOR guard can re-assert
// it even though loadCandidates already selects only bodyless rows.
type candidate struct {
	WorkID int64   `gorm:"column:work_id"`
	Workno string  `gorm:"column:workno"`
	Site   *string `gorm:"column:site"`
}

// loadCandidates resolves bodyless galgame works reachable via an EXACT DLsite
// workno release anchor:
//
//	catalog_work(bodyless galgame) → catalog_release(work_id)
//	  → catalog_external_ref(entity_type=release, source_id=dlsite, link_kind=exact, external_id=workno)
//
// DISTINCT ON keeps ONE workno per work (the lowest) — a work with several DLsite
// releases sources its media from one anchor. Ordered + windowed for chunking.
func loadCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]candidate, error) {
	q := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno, w.site AS site
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium)
	var out []candidate
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	// Window in Go after DISTINCT ON so the offset/limit apply to distinct works
	// (DISTINCT ON + LIMIT in one query can only window on the DISTINCT order key,
	// which is exactly w.id here — but slicing keeps chunking obviously correct).
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
