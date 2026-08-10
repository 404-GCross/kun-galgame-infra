// Package releaselabels materialises RELEASE-level attribution (wave 200):
// vndb releases_producers dev/pub flags → catalog_release_label.
//
// The work-level lane (internal/jobs/workproducers) answers "who made this
// work" and must stay narrow to mean anything. Everyone else the sources record
// — the console-port publisher, the localisation house, the fan-patch group —
// is a fact about ONE EDITION, and until now had nowhere to live. Flattened
// upward they turned a company list into a crowd; dropped they were simply lost.
// This is the shelf they belong on.
//
//   - Grain: RELEASE-level, NO language gate. That is the point: a release
//     already says which language and edition it is, so the reader can see that
//     Sekai Project published the English one without being told it co-made the
//     Japanese original.
//   - Kinds: developer → WorkLabelKindDeveloper, publisher → WorkLabelKindPublisher
//     (the same vocabulary as the work-level edge — a second enum meaning the
//     same thing would drift). source_id = vndb.
//   - Resolution: pid → catalog_label through EXACT vndb label anchors only.
//     Pids without one are COUNTED, never guessed — the same doctrine E2a set,
//     and the gap is structural rather than a re-run away.
//
// Edges are static facts — ON CONFLICT DO NOTHING; re-runs are no-ops.
package releaselabels

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

// Opts configures a run.
type Opts struct {
	Apply bool
	DSN   string // catalog DB (hosts src_vndb) — REQUIRED
}

// Stats reports a run. Planned counters are identical in dry and apply.
type Stats struct {
	DevPlanned int // (release,label) pairs with the developer flag
	PubPlanned int // (release,label) pairs with the publisher flag
	Written    int
	SkippedDup int // edge row already there (re-run)
	Unresolved int // (release,pid) pairs whose pid has no exact vndb label anchor
	Errors     int
}

// Run executes the backfill.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	db, err := database.OpenJob(opts.DSN)
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

	// Candidates: one row per (release,label) with OR-folded flags. vndb can
	// list a producer on a release twice under different roles, and the fold is
	// what turns that into "developer AND publisher" rather than two arguments.
	var cands []struct {
		ReleaseID int64 `gorm:"column:release_id"`
		LabelID   int64 `gorm:"column:label_id"`
		Developer bool  `gorm:"column:developer"`
		Publisher bool  `gorm:"column:publisher"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT rel.id AS release_id, lr.entity_id AS label_id,
		       bool_or(rp.developer) AS developer, bool_or(rp.publisher) AS publisher
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		JOIN catalog_external_ref lr ON lr.entity_type = ? AND lr.source_id = ?
		     AND lr.link_kind = 0 AND lr.external_id = rp.pid
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		GROUP BY 1, 2
		ORDER BY 1, 2`,
		model.EntityTypeLabel, vndbID, model.EntityTypeRelease, vndbID).Scan(&cands).Error; err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	// The unresolved tail — reported so the gap is a number rather than silence.
	if err := db.WithContext(ctx).Raw(`
		SELECT count(DISTINCT (rel.id, rp.pid))
		FROM catalog_external_ref r
		JOIN catalog_release rel ON rel.id = r.entity_id AND rel.deleted_at IS NULL
		JOIN src_vndb.releases_producers rp ON rp.id = r.external_id
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = 0
		  AND NOT EXISTS (SELECT 1 FROM catalog_external_ref lr WHERE lr.entity_type = ?
		                  AND lr.source_id = ? AND lr.link_kind = 0 AND lr.external_id = rp.pid)`,
		model.EntityTypeRelease, vndbID, model.EntityTypeLabel, vndbID).Scan(&st.Unresolved).Error; err != nil {
		return nil, fmt.Errorf("count unresolved: %w", err)
	}

	src := vndbID
	var rows []model.CatalogReleaseLabel
	for _, c := range cands {
		if c.Developer {
			st.DevPlanned++
			rows = append(rows, model.CatalogReleaseLabel{
				ReleaseID: c.ReleaseID, LabelID: c.LabelID, Kind: model.WorkLabelKindDeveloper, SourceID: &src,
			})
		}
		if c.Publisher {
			st.PubPlanned++
			rows = append(rows, model.CatalogReleaseLabel{
				ReleaseID: c.ReleaseID, LabelID: c.LabelID, Kind: model.WorkLabelKindPublisher, SourceID: &src,
			})
		}
	}

	// No TouchWorks: this is a one-off materialisation of facts the sources
	// already held, and bumping ~150k hosts would drown the public changes feed
	// in a day that saw no editorial change at all.
	if opts.Apply {
		for start := 0; start < len(rows); start += 2000 {
			end := min(start+2000, len(rows))
			res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
				CreateInBatches(rows[start:end], 2000)
			if res.Error != nil {
				st.Errors++
				slog.Warn("edge batch insert", "start", start, "err", res.Error)
				continue
			}
			st.Written += int(res.RowsAffected)
		}
		st.SkippedDup = st.DevPlanned + st.PubPlanned - st.Written - st.Errors
	}
	return st, nil
}
