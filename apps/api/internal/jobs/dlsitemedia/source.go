package dlsitemedia

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

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

// claimedSite is the CLAIM PREDICATE, not a site name. Being claimed is a
// property — catalog_work.site is non-empty — and wave 161 renamed the only
// value that has ever existed (galgame_wiki → kungal). A lane pinned to the old
// literal does not fail; it silently matches nothing and reports a clean
// zero-candidate run, which is how the intromt claimed lane sat dead for three
// days while looking healthy.
const claimedSite = `w.site IS NOT NULL AND w.site <> ''`

type candidate struct {
	WorkID int64   `gorm:"column:work_id"`
	Workno string  `gorm:"column:workno"`
	Site   *string `gorm:"column:site"`
}

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
	if kinds.Intro {
		claimed, err := loadClaimedIntroCandidates(ctx, db, reg)
		if err != nil {
			return nil, fmt.Errorf("claimed intro lane: %w", err)
		}
		out = append(out, claimed...)
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

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

func loadClaimedIntroCandidates(ctx context.Context, db *gorm.DB, reg registry) ([]candidate, error) {
	var out []candidate
	err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno, w.site AS site
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND w.deleted_at IS NULL
				AND w.site IS NOT NULL AND w.site <> '' AND w.product_work_id IS NOT NULL
				AND (w.claim_state IS NULL OR w.claim_state = 0)
				AND NOT EXISTS (SELECT 1 FROM catalog_work_intro i
					WHERE i.work_id = w.id AND i.lang = ?)
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium, langJa).
		Scan(&out).Error
	return out, err
}

func loadClaimedScreenshotCandidates(ctx context.Context, db *gorm.DB, reg registry) ([]candidate, error) {
	var out []candidate
	err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno, w.site AS site
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND `+claimedSite+` AND w.deleted_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM catalog_work_screenshot cs
					WHERE cs.work_id = w.id AND cs.source_id = ?)
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium, reg.dlsiteSource).
		Scan(&out).Error
	return out, err
}

type dlsiteMeta struct {
	Age         string
	Intro       string
	CoverFile   string
	SampleFiles []string
}

type dlsiteRow struct {
	Workno      string `gorm:"column:workno"`
	AgeCategory string `gorm:"column:age_category"`
	ProductJSON []byte `gorm:"column:product_json"`
	PageJSON    []byte `gorm:"column:page_json"`
}

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

type existing struct {
	intro map[int64]bool
	cover map[int64]bool
	shot  map[int64]map[int]bool
}

func preloadExisting(ctx context.Context, db *gorm.DB, workIDs []int64, sourceID int16, lang string) (*existing, error) {
	e := &existing{intro: map[int64]bool{}, cover: map[int64]bool{}, shot: map[int64]map[int]bool{}}
	if len(workIDs) == 0 {
		return e, nil
	}
	db = db.WithContext(ctx)

	var introWorks []int64
	if err := db.Raw(`SELECT work_id FROM catalog_work_intro WHERE lang = ? AND work_id IN ?`,
		lang, workIDs).Scan(&introWorks).Error; err != nil {
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
