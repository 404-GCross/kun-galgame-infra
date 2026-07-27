// Package workproducers lands the E2b producer-edge backfill (step 100,
// refs/proj/100): vndb releases_producers dev/pub flags → work-level
// catalog_work_label attribution edges.
//
//   - Grain: WORK-level, original-language gate. "Who developed / published
//     this" is a work-level consumer question, and vndb's own VN page derives
//     its producer list from original-language releases only. A localization
//     publisher (Sekai Project on the EN release) is a RELEASE-level fact —
//     deliberately NOT flattened into the work face here.
//   - Kinds: developer → WorkLabelKindDeveloper (2, reserved since E2, first
//     use); publisher → WorkLabelKindPublisher (1). source_id = vndb.
//   - Resolution: pid → catalog_label through the EXACT vndb label anchors
//     E2a graded (probable stays in the review lane). Pids without an exact
//     anchor are counted, never guessed (measured: the gap is structural —
//     an E2a re-run is a no-op; type=in never mints per doctrine).
//
// Edges are static facts — ON CONFLICT DO NOTHING; re-runs are no-ops.
package workproducers

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// Opts configures a run.
type Opts struct {
	Apply bool
	DSN   string // catalog DB (hosts src_vndb) — REQUIRED
}

// Stats reports a run. Planned counters are identical in dry and apply.
type Stats struct {
	DevPlanned int // (work,label) pairs with the developer flag
	PubPlanned int // (work,label) pairs with the publisher flag
	Written    int
	SkippedDup int // edge row already there (E2a mint or re-run)
	Unresolved int // (work,pid) pairs whose pid has no exact vndb label anchor
	Errors     int
}

// Run executes the backfill.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	db, err := gorm.Open(postgres.Open(opts.DSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}()

	var vndbID int16
	if err := db.WithContext(ctx).Raw(
		`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&vndbID).Error; err != nil || vndbID == 0 {
		return nil, fmt.Errorf("resolve vndb source id: %v", err)
	}

	st := &Stats{}

	// Candidates: one row per (work,label) with OR-folded flags. The olang
	// EXISTS is the original-language gate (non-MTL title row in w.olang).
	var cands []struct {
		WorkID    int64 `gorm:"column:work_id"`
		LabelID   int64 `gorm:"column:label_id"`
		Developer bool  `gorm:"column:developer"`
		Publisher bool  `gorm:"column:publisher"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT rel.work_id, lr.entity_id AS label_id,
		       bool_or(rp.developer) AS developer, bool_or(rp.publisher) AS publisher
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN catalog_work w ON w.id = rel.work_id AND w.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		JOIN catalog_external_ref lr ON lr.entity_type = ? AND lr.source_id = ?
		     AND lr.link_kind = 0 AND lr.external_id = rp.pid
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		  AND EXISTS (SELECT 1 FROM src_vndb.releases_titles rt
		              WHERE rt.id = r.external_id AND rt.lang = w.olang AND NOT rt.mtl)
		GROUP BY 1, 2
		ORDER BY 1, 2`,
		model.EntityTypeLabel, vndbID, model.EntityTypeRelease, vndbID).Scan(&cands).Error; err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	// The unresolved tail — counted for the report, never guessed.
	if err := db.WithContext(ctx).Raw(`
		SELECT count(DISTINCT (rel.work_id, rp.pid))
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN catalog_work w ON w.id = rel.work_id AND w.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		  AND EXISTS (SELECT 1 FROM src_vndb.releases_titles rt
		              WHERE rt.id = r.external_id AND rt.lang = w.olang AND NOT rt.mtl)
		  AND NOT EXISTS (SELECT 1 FROM catalog_external_ref lr WHERE lr.entity_type = ?
		                  AND lr.source_id = ? AND lr.link_kind = 0 AND lr.external_id = rp.pid)`,
		model.EntityTypeRelease, vndbID, model.EntityTypeLabel, vndbID).Scan(&st.Unresolved).Error; err != nil {
		return nil, fmt.Errorf("count unresolved: %w", err)
	}

	src := vndbID
	var rows []model.CatalogWorkLabel
	for _, c := range cands {
		if c.Developer {
			st.DevPlanned++
			rows = append(rows, model.CatalogWorkLabel{
				WorkID: c.WorkID, LabelID: c.LabelID, Kind: model.WorkLabelKindDeveloper, SourceID: &src,
			})
		}
		if c.Publisher {
			st.PubPlanned++
			rows = append(rows, model.CatalogWorkLabel{
				WorkID: c.WorkID, LabelID: c.LabelID, Kind: model.WorkLabelKindPublisher, SourceID: &src,
			})
		}
	}

	if opts.Apply {
		for start := 0; start < len(rows); start += 2000 {
			end := min(start+2000, len(rows))
			batch := rows[start:end]
			res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&batch)
			if res.Error != nil {
				st.Errors++
				slog.Warn("edge batch insert", "start", start, "err", res.Error)
				continue
			}
			st.Written += int(res.RowsAffected)
		}
		st.SkippedDup = st.DevPlanned + st.PubPlanned - st.Written
	}

	slog.Info("workproducers done", "apply", opts.Apply,
		"dev_planned", st.DevPlanned, "pub_planned", st.PubPlanned,
		"written", st.Written, "skipped_dup", st.SkippedDup,
		"unresolved_pairs", st.Unresolved, "errors", st.Errors)
	return st, nil
}
