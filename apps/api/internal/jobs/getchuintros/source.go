package getchuintros

import (
	"context"
	"fmt"

	"api/internal/jobs/workpop"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// anchorRow is one (work → Getchu item) link. A work can carry several Getchu
// releases (a regular edition, a DL edition, a fandisc bundle), so this is
// deliberately NOT deduplicated in SQL: only the staging side knows which of
// them actually has a story, and that lives in another database. pickStory does
// the choosing.
type anchorRow struct {
	WorkID   int64  `gorm:"column:work_id"`
	GetchuID string `gorm:"column:getchu_id"`
}

// loadAnchors resolves the works in the requested population that carry an
// EXACT Getchu release anchor:
//
//	catalog_work → catalog_release → catalog_external_ref(entity_type=release,
//	  source=getchu, link_kind=exact)
//
// Exact only. The wave-167 anchors were minted through VNDB extlinks, so an
// exact Getchu ref is a first-party identity assertion; probable anchors sit in
// the confirm bucket and this lane never reads them.
func loadAnchors(ctx context.Context, db *gorm.DB, source int16, pop workpop.Population, limit, offset int) ([]anchorRow, error) {
	site, err := workpop.Predicate(pop, "w")
	if err != nil {
		return nil, err
	}
	var out []anchorRow
	err = db.WithContext(ctx).Raw(`
		SELECT DISTINCT w.id AS work_id, r.external_id AS getchu_id
		FROM catalog_work w
		JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
			AND r.source_id = ? AND r.link_kind = ?
		WHERE w.deleted_at IS NULL AND `+site+`
		ORDER BY w.id, r.external_id`,
		model.EntityTypeRelease, source, model.LinkKindExact).Scan(&out).Error
	if err != nil {
		return nil, fmt.Errorf("load getchu anchors: %w", err)
	}
	return window(out, limit, offset), nil
}

// window applies offset/limit over whole WORKS, not raw anchor rows — a work
// whose two releases straddled a chunk boundary would otherwise be processed
// twice and counted twice.
func window(rows []anchorRow, limit, offset int) []anchorRow {
	if limit <= 0 && offset <= 0 {
		return rows
	}
	var (
		out      []anchorRow
		nth      int // 1-based index of the work the current row belongs to
		lastWork int64
	)
	for _, r := range rows {
		if r.WorkID != lastWork {
			lastWork = r.WorkID
			nth++
			if limit > 0 && nth > offset+limit {
				break
			}
		}
		if nth > offset {
			out = append(out, r)
		}
	}
	return out
}

// loadStories reads the crawler's staging database.
//
// THE COLUMN IS `story`, NOT `intro`. The staging table has both and the name
// is a trap: `intro` is Getchu's storefront news blurb — 14 rows in the whole
// 19,994-item table, saying things like「マスターアップ画像いただきました！」
// (a master-up announcement) or describing a pre-order illustration card. The
// description a reader wants is `story`: 12,565 rows, averaging 399 characters,
// surveyed clean (zero HTML tags, zero CR). The two columns never co-occur.
func loadStories(ctx context.Context, gdb *gorm.DB) (map[string]string, error) {
	var rows []struct {
		GetchuID string `gorm:"column:getchu_id"`
		Story    string `gorm:"column:story"`
	}
	err := gdb.WithContext(ctx).Raw(`
		SELECT getchu_id, story FROM items WHERE btrim(coalesce(story,'')) <> ''`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read staging items: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.GetchuID] = r.Story
	}
	return out, nil
}

// preloadExistingLangs loads every (work → intro langs) pair already present
// for the candidate works, across ALL sources: the fill-missing rule asks "does
// this work have Japanese at all?", not "did getchu already write it?". This is
// the primary skip; the (work_id,lang,source_id) ON CONFLICT is the backstop.
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
