// Package galgamestats is the shared logic behind the build-galgame-stats CLI
// and the scheduled "build-galgame-stats" job: it recomputes the cross-source
// galgame statistics snapshots (galgame_stats) wholesale + idempotently from
// the wiki DB's four tables (galgame + the three narrow score tables), then
// upserts one row per stable snake_case key. No serving surface reads the
// mirror — that is step 34; this only materializes the snapshot.
//
// Discipline (00-workflow §10): statistics count only real scores. VNDB/EG
// ratings are NULL when unrated → excluded; Bangumi score is a non-null float
// where 0 means "unrated" → score>0 is the "rated" predicate. A source with
// zero rows (local bangumi/EG before their production enrichment) yields an
// empty array / zero coverage, never an error.
package galgamestats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Snapshot key set v1 — FROZEN (these keys are the step-34 public contract
// candidate). Payload shapes are pinned by the structs below.
const (
	keyReleaseYears  = "release_years"
	keyHistogramVNDB = "score_histogram_vndb"
	keyHistogramBGM  = "score_histogram_bangumi"
	keyHistogramEG   = "score_histogram_eg"
	keyYearlyScores  = "yearly_scores"
	keyCoverage      = "coverage"
)

// The year cohort: a galgame counts toward year aggregates only when its
// release date is known to at least year precision and not TBA. release_date is
// normalized (day/month unknown → 1st), so precision is the source of truth.
const yearCohort = `release_date IS NOT NULL AND release_precision IN ('day','month','year') AND release_date_tba = false`

// Opts controls a build.
type Opts struct {
	Apply bool // false = compute + log only, no writes
}

// --- payload shapes (snake_case JSON; 34's contract candidate) ---

// yearCount is one row of release_years: how many galgames released that year.
type yearCount struct {
	Year  int `json:"year"`
	Count int `json:"count"`
}

// histBucket is one half-open [lo, hi) score bucket (last bucket includes hi).
type histBucket struct {
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Count int     `json:"count"`
}

// histogram is a score distribution over a source's native domain; total = the
// number of rated rows binned (matches sum of bucket counts).
type histogram struct {
	Buckets []histBucket `json:"buckets"`
	Total   int          `json:"total"`
}

// yearScore is one row of yearly_scores: per-source average + sample size for
// games released that year. avg is null when n=0 (never a fake zero); the
// presentation threshold on small n is left to step 34 / the frontend.
type yearScore struct {
	Year       int      `json:"year"`
	VNDBAvg    *float64 `json:"vndb_avg"`
	VNDBN      int      `json:"vndb_n"`
	BangumiAvg *float64 `json:"bangumi_avg"`
	BangumiN   int      `json:"bangumi_n"`
	EGAvg      *float64 `json:"eg_avg"`
	EGN        int      `json:"eg_n"`
}

// coverage is the one-shot completeness summary across the four tables.
type coverage struct {
	Total        int `json:"total"`
	VNDBRows     int `json:"vndb_rows"`
	VNDBRated    int `json:"vndb_rated"`
	BangumiRows  int `json:"bangumi_rows"`
	BangumiRated int `json:"bangumi_rated"`
	EGRows       int `json:"eg_rows"`
	EGRated      int `json:"eg_rated"`
}

// Run connects to the wiki DB and builds the snapshots. apply=false computes +
// logs but writes nothing.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer db.Close()
	return Build(ctx, db.DB(), opts)
}

// Build computes every snapshot key from the wiki DB and (when Apply) upserts
// them into galgame_stats under one shared built_at. Exposed so the CLI, the
// job, and the integration test all drive identical logic.
func Build(ctx context.Context, db *gorm.DB, opts Opts) (map[string]any, error) {
	releaseYears, err := computeReleaseYears(db)
	if err != nil {
		return nil, fmt.Errorf("release_years: %w", err)
	}
	vndbHist, err := computeHistogram(db, "galgame_vndb_meta", "rating", "rating IS NOT NULL", 10, 100)
	if err != nil {
		return nil, fmt.Errorf("score_histogram_vndb: %w", err)
	}
	bgmHist, err := computeHistogram(db, "galgame_bangumi_meta", "score", "score > 0", 0, 10)
	if err != nil {
		return nil, fmt.Errorf("score_histogram_bangumi: %w", err)
	}
	egHist, err := computeHistogram(db, "galgame_eg_meta", "median", "median IS NOT NULL", 0, 100)
	if err != nil {
		return nil, fmt.Errorf("score_histogram_eg: %w", err)
	}
	yearly, err := computeYearlyScores(db)
	if err != nil {
		return nil, fmt.Errorf("yearly_scores: %w", err)
	}
	cov, err := computeCoverage(db)
	if err != nil {
		return nil, fmt.Errorf("coverage: %w", err)
	}

	now := time.Now()
	snapshots := []model.GalgameStats{
		mustSnap(keyReleaseYears, releaseYears, now),
		mustSnap(keyHistogramVNDB, vndbHist, now),
		mustSnap(keyHistogramBGM, bgmHist, now),
		mustSnap(keyHistogramEG, egHist, now),
		mustSnap(keyYearlyScores, yearly, now),
		mustSnap(keyCoverage, cov, now),
	}

	if opts.Apply {
		for _, s := range snapshots {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := upsertSnapshot(db, s); err != nil {
				return nil, fmt.Errorf("upsert %s: %w", s.Key, err)
			}
		}
	}

	summary := map[string]any{
		"keys":              len(snapshots),
		"release_years":     len(releaseYears),
		"vndb_rated":        vndbHist.Total,
		"bangumi_rated":     bgmHist.Total,
		"eg_rated":          egHist.Total,
		"yearly_score_rows": len(yearly),
		"total":             cov.Total,
		"applied":           opts.Apply,
	}
	slog.Info("build-galgame-stats done", "keys", summary["keys"], "total", cov.Total,
		"vndb_rated", vndbHist.Total, "bangumi_rated", bgmHist.Total, "eg_rated", egHist.Total,
		"release_years", len(releaseYears), "yearly_rows", len(yearly), "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary, nil
}

func computeReleaseYears(db *gorm.DB) ([]yearCount, error) {
	// Raw SQL so the positional GROUP BY / ORDER BY stay literal (GORM's Group
	// helper would quote "1" into a column identifier).
	var out []yearCount
	err := db.Raw(`
		SELECT EXTRACT(YEAR FROM release_date)::int AS year, count(*) AS count
		FROM galgame
		WHERE ` + yearCohort + `
		GROUP BY 1 ORDER BY 1`).Scan(&out).Error
	if out == nil {
		out = []yearCount{}
	}
	return out, err
}

// computeHistogram bins a source's rated scores into 10 equal-width buckets over
// [lo, hi] (VNDB native 10-100 → width 9; Bangumi 0-10 → width 1; EG 0-100 →
// width 10). Values are pulled and binned in Go so the bucketing is one tested
// helper across all three sources. total = number of rated rows.
func computeHistogram(db *gorm.DB, table, col, rated string, lo, hi float64) (histogram, error) {
	var vals []float64
	if err := db.Table(table).Where(rated).Pluck(col, &vals).Error; err != nil {
		return histogram{}, err
	}
	const n = 10
	width := (hi - lo) / n
	buckets := make([]histBucket, n)
	for i := range buckets {
		buckets[i] = histBucket{Lo: lo + width*float64(i), Hi: lo + width*float64(i+1)}
	}
	for _, v := range vals {
		idx := int((v - lo) / width)
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1 // v == hi lands in the last bucket
		}
		buckets[idx].Count++
	}
	return histogram{Buckets: buckets, Total: len(vals)}, nil
}

// computeYearlyScores emits one row per year that has at least one rated score
// in any source, with each source's average + sample size over games released
// that year (avg null when that source has no rated games that year).
func computeYearlyScores(db *gorm.DB) ([]yearScore, error) {
	type agg struct {
		Year int
		Avg  float64
		N    int
	}
	// Raw SQL (literal positional GROUP BY) with avg cast to float8 so an
	// integer column's numeric average scans cleanly into float64. table/col/
	// rated are fixed internal constants, never user input.
	load := func(table, col, rated string) (map[int]agg, error) {
		var rows []agg
		err := db.Raw(`
			SELECT EXTRACT(YEAR FROM g.release_date)::int AS year,
			       avg(m.` + col + `)::float8 AS avg, count(*) AS n
			FROM galgame AS g
			JOIN ` + table + ` AS m ON m.galgame_id = g.id
			WHERE ` + prefixCohort("g") + ` AND m.` + rated + `
			GROUP BY 1`).Scan(&rows).Error
		m := map[int]agg{}
		for _, r := range rows {
			m[r.Year] = r
		}
		return m, err
	}
	vndb, err := load("galgame_vndb_meta", "rating", "rating IS NOT NULL")
	if err != nil {
		return nil, err
	}
	bgm, err := load("galgame_bangumi_meta", "score", "score > 0")
	if err != nil {
		return nil, err
	}
	eg, err := load("galgame_eg_meta", "median", "median IS NOT NULL")
	if err != nil {
		return nil, err
	}

	yearSet := map[int]bool{}
	for y := range vndb {
		yearSet[y] = true
	}
	for y := range bgm {
		yearSet[y] = true
	}
	for y := range eg {
		yearSet[y] = true
	}
	years := make([]int, 0, len(yearSet))
	for y := range yearSet {
		years = append(years, y)
	}
	sort.Ints(years)

	out := make([]yearScore, 0, len(years))
	for _, y := range years {
		ys := yearScore{Year: y}
		if a, ok := vndb[y]; ok {
			ys.VNDBAvg, ys.VNDBN = round2(a.Avg), a.N
		}
		if a, ok := bgm[y]; ok {
			ys.BangumiAvg, ys.BangumiN = round2(a.Avg), a.N
		}
		if a, ok := eg[y]; ok {
			ys.EGAvg, ys.EGN = round2(a.Avg), a.N
		}
		out = append(out, ys)
	}
	return out, nil
}

func computeCoverage(db *gorm.DB) (coverage, error) {
	var c coverage
	err := db.Raw(`SELECT
		(SELECT count(*) FROM galgame),
		(SELECT count(*) FROM galgame_vndb_meta),
		(SELECT count(*) FROM galgame_vndb_meta WHERE rating IS NOT NULL),
		(SELECT count(*) FROM galgame_bangumi_meta),
		(SELECT count(*) FROM galgame_bangumi_meta WHERE score > 0),
		(SELECT count(*) FROM galgame_eg_meta),
		(SELECT count(*) FROM galgame_eg_meta WHERE median IS NOT NULL)`).
		Row().Scan(&c.Total, &c.VNDBRows, &c.VNDBRated, &c.BangumiRows, &c.BangumiRated, &c.EGRows, &c.EGRated)
	return c, err
}

// prefixCohort rewrites the cohort predicate onto an aliased galgame table.
func prefixCohort(alias string) string {
	return alias + `.release_date IS NOT NULL AND ` + alias + `.release_precision IN ('day','month','year') AND ` + alias + `.release_date_tba = false`
}

func round2(v float64) *float64 {
	r := float64(int64(v*100+0.5)) / 100
	return &r
}

func mustSnap(key string, payload any, now time.Time) model.GalgameStats {
	b, err := json.Marshal(payload)
	if err != nil {
		// The payload types are fixed structs — marshaling cannot fail; panic
		// makes a programmer error loud rather than shipping a corrupt snapshot.
		panic(fmt.Sprintf("marshal %s snapshot: %v", key, err))
	}
	return model.GalgameStats{Key: key, Payload: datatypes.JSON(b), BuiltAt: now}
}

func upsertSnapshot(db *gorm.DB, row model.GalgameStats) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"payload", "built_at"}),
	}).Create(&row).Error
}
