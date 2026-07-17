// Package introimport backfills catalog_work_intro rows for BODYLESS works
// from an upstream source's description (step 52 media-aggregation pilot,
// refs/proj/51). This wave's only source is VNDB vn.description; a bodyless
// galgame work that holds an EXACT VNDB work-level anchor gets its blurb copied
// into a native intro row (lang='en' — VNDB writes descriptions in English by
// convention), attributed to the vndb source.
//
// Discipline (§8.B shadow-never-delete + idempotency): writes are INSERT-only
// with ON CONFLICT (work_id, lang, source_id) DO NOTHING, so a second run is a
// no-op. CLAIMED works are never touched (their intro is bridged at read time,
// never materialized — bridge-not-copy, §2). Description is stored VERBATIM;
// cleaning/translation is explicitly out of scope for the pilot (§5).
package introimport

import (
	"context"
	"fmt"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// introLang is the language of a VNDB description — English, always (the VN's
// own olang is a different fact and is NOT the blurb's language).
const introLang = "en"

// Options configures a backfill run.
type Options struct {
	DryRun bool // when true, count what WOULD be written but write nothing
	Limit  int  // max candidate works to consider (0 = all)
}

// Stats is the backfill outcome. IntrosWritten counts rows inserted (or, in a
// dry run, that WOULD be inserted). WorksCovered is the distinct works that
// received at least one intro.
type Stats struct {
	TotalBodyless    int64 // bodyless galgame works in scope
	WithVNDBAnchor   int64 // …that hold an exact VNDB work anchor
	SkippedNoAnchor  int64 // …that do NOT (this wave's only source is VNDB)
	SkippedEmptyDesc int64 // anchored but VNDB has no/empty description
	Already          int64 // (work, en, vndb) row already present
	IntrosWritten    int64 // new rows written (or would-be, in a dry run)
	WorksCovered     int64 // distinct works that got an intro
}

// candidate is one bodyless galgame work reached via its VNDB anchor.
type candidate struct {
	WorkID      int64  `gorm:"column:work_id"`
	VNDBID      string `gorm:"column:vndb_id"`
	Description string `gorm:"column:description"`
}

// Run performs (or dry-runs) the backfill. It resolves the galgame medium and
// vndb source by key (no hardcoded ids), gathers candidates in one query, and
// writes the missing intro rows in one batched INSERT ... ON CONFLICT DO NOTHING.
func Run(ctx context.Context, db *gorm.DB, opts Options) (Stats, error) {
	db = db.WithContext(ctx)
	var st Stats

	var mediumID int16
	if err := db.Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&mediumID).Error; err != nil {
		return st, fmt.Errorf("resolve galgame medium: %w", err)
	}
	var vndbSourceID int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&vndbSourceID).Error; err != nil {
		return st, fmt.Errorf("resolve vndb source: %w", err)
	}
	if mediumID == 0 || vndbSourceID == 0 {
		return st, fmt.Errorf("registry not seeded (galgame medium=%d, vndb source=%d)", mediumID, vndbSourceID)
	}

	// Total bodyless galgame works (site NULL or '').
	if err := db.Raw(`SELECT count(*) FROM catalog_work
		WHERE medium_id = ? AND (site IS NULL OR site = '') AND deleted_at IS NULL`,
		mediumID).Scan(&st.TotalBodyless).Error; err != nil {
		return st, fmt.Errorf("count bodyless: %w", err)
	}

	// Candidates: bodyless galgame works with an EXACT VNDB work anchor, LEFT
	// JOINed to the staged vn row (so an anchor with no vn row still surfaces,
	// counted as an empty description). DISTINCT ON keeps one anchor per work
	// (the lowest vndb id) — a work with two VNDB anchors can hold only one
	// (work, en, vndb) row.
	q := `SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS vndb_id, COALESCE(v.description, '') AS description
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id AND r.link_kind = ?
		JOIN catalog_source s ON s.id = r.source_id AND s.id = ?
		LEFT JOIN src_vndb.vn v ON v.id = r.external_id
		WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
		ORDER BY w.id, r.external_id`
	var cands []candidate
	if err := db.Raw(q, model.EntityTypeWork, model.LinkKindExact, vndbSourceID, mediumID).Scan(&cands).Error; err != nil {
		return st, fmt.Errorf("gather candidates: %w", err)
	}
	if opts.Limit > 0 && len(cands) > opts.Limit {
		cands = cands[:opts.Limit]
	}
	st.WithVNDBAnchor = int64(len(cands))
	st.SkippedNoAnchor = st.TotalBodyless - st.WithVNDBAnchor

	// Which candidate works already carry the (en, vndb) row.
	workIDs := make([]int64, 0, len(cands))
	for _, c := range cands {
		workIDs = append(workIDs, c.WorkID)
	}
	already := map[int64]bool{}
	if len(workIDs) > 0 {
		var existing []int64
		if err := db.Raw(`SELECT work_id FROM catalog_work_intro
			WHERE lang = ? AND source_id = ? AND work_id IN ?`, introLang, vndbSourceID, workIDs).Scan(&existing).Error; err != nil {
			return st, fmt.Errorf("load existing: %w", err)
		}
		for _, id := range existing {
			already[id] = true
		}
	}

	var toWrite []model.CatalogWorkIntro
	for _, c := range cands {
		if strings.TrimSpace(c.Description) == "" {
			st.SkippedEmptyDesc++
			continue
		}
		if already[c.WorkID] {
			st.Already++
			continue
		}
		toWrite = append(toWrite, model.CatalogWorkIntro{
			WorkID: c.WorkID, Lang: introLang, Intro: c.Description, SourceID: vndbSourceID,
		})
	}

	st.IntrosWritten = int64(len(toWrite))
	st.WorksCovered = st.IntrosWritten // one language per work in this wave
	if opts.DryRun || len(toWrite) == 0 {
		return st, nil
	}

	res := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "lang"}, {Name: "source_id"}},
		DoNothing: true,
	}).CreateInBatches(toWrite, 500)
	if res.Error != nil {
		return st, fmt.Errorf("insert intros: %w", res.Error)
	}
	// RowsAffected is the true insert count (ON CONFLICT skips are not counted),
	// so it reflects a concurrent writer or a partially-applied prior run.
	st.IntrosWritten = res.RowsAffected
	st.WorksCovered = res.RowsAffected
	return st, nil
}
