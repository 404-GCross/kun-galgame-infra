package workratings

import (
	"context"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// writer applies planned rating rows with the write-time XOR guard and the
// ON CONFLICT idempotency backstop (serial, plain ints).
type writer struct {
	db    *gorm.DB
	stats *Stats
}

// plannedRow is one decided catalog_work_rating write, carrying the work's
// site so the guard can re-assert bodylessness at the last moment.
type plannedRow struct {
	WorkID    int64
	Site      *string
	SourceID  int16
	Score     float64
	VoteCount int
	Rank      *int
}

// write enforces the XOR guard, then (apply only) inserts the row. written /
// conflict point at the owning lane's counters so both lanes share one path.
func (w *writer) write(ctx context.Context, p plannedRow, apply bool, written, conflict *int) {
	if !isBodyless(p.Site) { // XOR guard (§8.D) — never materialise a claimed work
		w.stats.Refused++
		return
	}
	if !apply {
		return
	}
	res := w.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkRating{
		WorkID: p.WorkID, SourceID: p.SourceID, Score: p.Score, VoteCount: p.VoteCount, Rank: p.Rank,
	})
	if res.Error != nil {
		w.stats.Errors++
		slog.Warn("write rating", "work", p.WorkID, "source", p.SourceID, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // row already there (second pass / concurrent writer)
		*conflict++
		return
	}
	*written++
}

// isBodyless reports whether a catalog_work is bodyless (site NULL or ”) —
// the §8.D claim key, same shape as bgmsummaries.
func isBodyless(site *string) bool { return site == nil || *site == "" }
