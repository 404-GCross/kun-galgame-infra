// Package workplaytime backfills catalog_work_playtime rows — the playtime
// facet (refs/proj/91, ledger 时长 row) — from the two sources that publish a
// playtime estimate:
//
//   - erogamespace lane: EG EXACT work anchors join the EG mirror's games
//     (--eg-dsn, a separate database); games whose raw->>'total_play_time_median'
//     is numeric land as an erogamespace row, HOURS ×60 → minutes.
//   - vndb lane: vndb EXACT work anchors join src_vndb.vn (a schema INSIDE the
//     catalog DB — single --dsn); VNs with a non-NULL c_length land as a vndb
//     row: minutes verbatim, vote_count = c_lengthnum.
//
// This facet has NO claimed bridge lane (the wiki galgame family carries no
// playtime field), so BOTH lanes admit claimed and bodyless works alike — the
// (facet,source) XOR's degenerate case (step-88 semantics: bridge set empty).
//
// Discipline (58a/62 lineage): every DSN explicit; dry-run default; writes are
// ON CONFLICT DO UPDATE with change detection (refresh-runnable — a mirror or
// dump refresh + re-run updates in place, an unchanged re-run is a no-op).
// Values above the sanity cap (1,000 hours — the mirror carries 10,000h-level
// garbage) are rejected and counted, never written.
package workplaytime

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strconv"

	"api/internal/platform/catalog/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// capMinutes rejects garbage estimates: 1,000 hours (the EG mirror's
// total_play_time_median carries values up to 10,000h).
const capMinutes = 1000 * 60

// Opts configures a run. Source selects the lane: "eg" | "vndb" | "all".
type Opts struct {
	Apply  bool
	DSN    string // catalog DB (hosts src_vndb) — REQUIRED
	EGDSN  string // EG mirror DB — REQUIRED for the eg/all lanes
	Source string
}

// Stats reports a run. Planned counters are identical in dry and apply.
type Stats struct {
	EGAnchored  int // works with an EG anchor whose game has a numeric playtime
	EGPlanned   int
	EGRejected  int // over-cap estimates dropped (counted per planned row)
	EGWritten   int
	EGUnchanged int

	VndbPlanned   int
	VndbRejected  int
	VndbWritten   int
	VndbUnchanged int

	Errors int
}

// Run executes the backfill.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.Source == "" {
		opts.Source = "all"
	}
	if (opts.Source == "eg" || opts.Source == "all") && opts.EGDSN == "" {
		return nil, fmt.Errorf("EG mirror DSN is required for the eg lane (--eg-dsn)")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer closeGorm(db)

	ids, err := resolveIDs(ctx, db)
	if err != nil {
		return nil, err
	}

	st := &Stats{}
	if opts.Source == "eg" || opts.Source == "all" {
		if err := runEG(ctx, db, opts, ids, st); err != nil {
			return nil, err
		}
	}
	if opts.Source == "vndb" || opts.Source == "all" {
		if err := runVndb(ctx, db, opts, ids, st); err != nil {
			return nil, err
		}
	}
	slog.Info("workplaytime done", "apply", opts.Apply,
		"eg_anchored", st.EGAnchored, "eg_planned", st.EGPlanned, "eg_rejected", st.EGRejected,
		"eg_written", st.EGWritten, "eg_unchanged", st.EGUnchanged,
		"vndb_planned", st.VndbPlanned, "vndb_rejected", st.VndbRejected,
		"vndb_written", st.VndbWritten, "vndb_unchanged", st.VndbUnchanged, "errors", st.Errors)
	return st, nil
}

type registryIDs struct {
	galgameMedium int16
	egSource      int16
	vndbSource    int16
}

func resolveIDs(ctx context.Context, db *gorm.DB) (registryIDs, error) {
	var r registryIDs
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'erogamespace'`).Scan(&r.egSource).Error; err != nil {
		return r, fmt.Errorf("resolve erogamespace source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&r.vndbSource).Error; err != nil {
		return r, fmt.Errorf("resolve vndb source: %w", err)
	}
	if r.galgameMedium == 0 || r.egSource == 0 || r.vndbSource == 0 {
		return r, fmt.Errorf("registry not seeded (medium=%d eg=%d vndb=%d)", r.galgameMedium, r.egSource, r.vndbSource)
	}
	return r, nil
}

// numRe admits the mirror's numeric playtime strings (integers in practice;
// tolerate a decimal tail defensively).
var numRe = regexp.MustCompile(`^\d+(\.\d+)?$`)

// runEG: anchors → mirror playtimes (hours) → ×60 → upsert.
func runEG(ctx context.Context, db *gorm.DB, opts Opts, ids registryIDs, st *Stats) error {
	anchors, err := loadAnchors(ctx, db, ids.galgameMedium, ids.egSource)
	if err != nil {
		return fmt.Errorf("load eg anchors: %w", err)
	}
	egDB, err := openGorm(opts.EGDSN)
	if err != nil {
		return fmt.Errorf("connect eg mirror: %w", err)
	}
	defer closeGorm(egDB)

	// Batch-load the mirror playtimes for the anchored game ids (numeric
	// external ids only — non-numeric are impossible for the EG lane but the
	// filter keeps the cast safe).
	gameIDs := make([]int64, 0, len(anchors))
	for _, a := range anchors {
		if n, err := strconv.ParseInt(a.ExternalID, 10, 64); err == nil {
			gameIDs = append(gameIDs, n)
		}
	}
	playtime := map[int64]float64{} // game id → hours
	for _, chunk := range chunkInt64(gameIDs, 10000) {
		var rows []struct {
			ID int64  `gorm:"column:id"`
			PT string `gorm:"column:pt"`
		}
		if err := egDB.WithContext(ctx).
			Raw(`SELECT id, raw->>'total_play_time_median' AS pt FROM games
				WHERE id IN ? AND raw->>'total_play_time_median' IS NOT NULL`, chunk).
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("load eg playtimes: %w", err)
		}
		for _, r := range rows {
			if !numRe.MatchString(r.PT) {
				continue
			}
			if h, err := strconv.ParseFloat(r.PT, 64); err == nil {
				playtime[r.ID] = h
			}
		}
	}

	// touched collects works whose playtime really moved, so the lane bumps their
	// catalog_work.updated_at once and the public changes feed sees it. Unchanged
	// upserts and dry-runs contribute nothing.
	var touched []int64
	for _, a := range anchors {
		n, err := strconv.ParseInt(a.ExternalID, 10, 64)
		if err != nil {
			continue
		}
		h, ok := playtime[n]
		if !ok {
			continue
		}
		st.EGAnchored++
		minutes := int(math.Round(h * 60))
		if minutes <= 0 {
			continue
		}
		if minutes > capMinutes {
			st.EGRejected++
			continue
		}
		st.EGPlanned++
		if !opts.Apply {
			continue
		}
		if upsert(ctx, db, a.WorkID, ids.egSource, minutes, 0, &st.EGWritten, &st.EGUnchanged, &st.Errors) {
			touched = append(touched, a.WorkID)
		}
	}
	return repository.TouchWorks(ctx, db, touched)
}

// runVndb: anchors ⋈ src_vndb.vn c_length (already minutes) → upsert.
func runVndb(ctx context.Context, db *gorm.DB, opts Opts, ids registryIDs, st *Stats) error {
	var rows []struct {
		WorkID    int64 `gorm:"column:work_id"`
		Minutes   int   `gorm:"column:minutes"`
		VoteCount int   `gorm:"column:vote_count"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (w.id) w.id AS work_id, v.c_length AS minutes, v.c_lengthnum AS vote_count
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_type = 5 AND r.entity_id = w.id
			AND r.source_id = ? AND r.link_kind = 0
		JOIN src_vndb.vn v ON v.id = r.external_id
		WHERE w.medium_id = ? AND w.deleted_at IS NULL AND v.c_length IS NOT NULL
		ORDER BY w.id, r.external_id`, ids.vndbSource, ids.galgameMedium).
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("load vndb playtimes: %w", err)
	}
	var touched []int64 // works whose playtime really moved (see runEG)
	for _, r := range rows {
		if r.Minutes <= 0 {
			continue
		}
		if r.Minutes > capMinutes {
			st.VndbRejected++
			continue
		}
		st.VndbPlanned++
		if !opts.Apply {
			continue
		}
		if upsert(ctx, db, r.WorkID, ids.vndbSource, r.Minutes, r.VoteCount, &st.VndbWritten, &st.VndbUnchanged, &st.Errors) {
			touched = append(touched, r.WorkID)
		}
	}
	return repository.TouchWorks(ctx, db, touched)
}

type anchor struct {
	WorkID     int64  `gorm:"column:work_id"`
	ExternalID string `gorm:"column:external_id"`
}

// loadAnchors resolves EXACT work anchors of a source on galgame works —
// claimed and bodyless alike (no XOR arm for this facet). DISTINCT ON keeps
// one anchor per work (lowest external_id, the workratings discipline).
func loadAnchors(ctx context.Context, db *gorm.DB, medium, source int16) ([]anchor, error) {
	var out []anchor
	if err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id
		FROM catalog_work w
		JOIN catalog_external_ref r ON r.entity_type = 5 AND r.entity_id = w.id
			AND r.source_id = ? AND r.link_kind = 0
		WHERE w.medium_id = ? AND w.deleted_at IS NULL
		ORDER BY w.id, r.external_id`, source, medium).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// upsert writes one row with change detection: insert, or update only when the
// stored (minutes, vote_count) differ — the second identical run writes zero.
// Reports whether the row actually moved, which is what the caller needs to
// decide whose catalog_work.updated_at to bump.
func upsert(ctx context.Context, db *gorm.DB, workID int64, sourceID int16, minutes, votes int, written, unchanged, errors *int) bool {
	res := db.WithContext(ctx).Exec(`
		INSERT INTO catalog_work_playtime (work_id, source_id, minutes, vote_count)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (work_id, source_id) DO UPDATE
			SET minutes = EXCLUDED.minutes, vote_count = EXCLUDED.vote_count, updated_at = now()
			WHERE (catalog_work_playtime.minutes, catalog_work_playtime.vote_count)
				IS DISTINCT FROM (EXCLUDED.minutes, EXCLUDED.vote_count)`,
		workID, sourceID, minutes, votes)
	if res.Error != nil {
		*errors++
		slog.Warn("playtime upsert", "work", workID, "source", sourceID, "err", res.Error)
		return false
	}
	if res.RowsAffected == 1 {
		*written++
		return true
	}
	*unchanged++
	return false
}

func chunkInt64(in []int64, size int) [][]int64 {
	var out [][]int64
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
