// migrate-galgame-release-precision backfills galgame.release_precision (added
// for the release calendar, see docs/galgame_wiki/06-release-calendar-design.md)
// on rows that predate the column. It runs in two passes:
//
//	Pass 1 (SQL, always): derive precision from the existing columns —
//	  release_date_tba = true            → 'tba'
//	  release_date IS NULL (& not tba)   → 'unknown'
//	  release_date with day <> 1         → 'day' (a non-1st date can only be exact)
//	  remaining dated rows (day = 1)     → 'day' (provisional; refined by pass 2)
//	Only rows still at the 'unknown' default are touched, so it is idempotent.
//
//	Pass 2 (VNDB, --vndb, default on): the only ambiguous rows are those whose
//	  release_date was normalized to the 1st of a month — they could be an exact
//	  1st, month-only (YYYY-MM-01), or year-only (YYYY-01-01). The original
//	  precision is gone from our DB (the legacy `released` string + snapshots
//	  were stripped), so for vndb-sourced rows we re-fetch VNDB's `released` and
//	  set the true precision, moving year-only games out of January.
//
// Dry run by default; --apply writes. Safe to re-run.
//
//	go run ./cmd/migrate-galgame-release-precision                   # dry run
//	go run ./cmd/migrate-galgame-release-precision --apply
//	go run ./cmd/migrate-galgame-release-precision --apply --vndb=false  # SQL pass only
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counts only)")
	useVNDB := flag.Bool("vndb", true, "pass 2: re-derive ambiguous 1st-of-month rows from VNDB")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)
	slog.Info("connecting to galgame wiki database", "dbname", cfg.GalgameDatabase.DBName, "apply", *apply, "vndb", *useVNDB)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	g := db.DB()

	if err := pass1SQL(g, *apply); err != nil {
		slog.Error("pass 1 failed", "error", err)
		os.Exit(1)
	}
	if *useVNDB {
		if err := pass2VNDB(ctx, g, vndb.New(*gap), *apply); err != nil {
			slog.Error("pass 2 failed", "error", err)
			os.Exit(1)
		}
	}
	slog.Info("release_precision backfill complete", "apply", *apply)
}

// pass1SQL fills precision from the columns we still have. Each rule is guarded
// so a re-run (or pass-2-corrected rows) is left untouched → idempotent.
func pass1SQL(g *gorm.DB, apply bool) error {
	rules := []struct{ label, where, set string }{
		{"tba", "release_date_tba = true AND release_precision <> 'tba'", "tba"},
		{"unknown", "release_date IS NULL AND release_date_tba = false AND release_precision <> 'unknown'", "unknown"},
		{"day", "release_date IS NOT NULL AND release_precision = 'unknown'", "day"},
	}
	for _, r := range rules {
		var n int64
		if err := g.Raw("SELECT count(*) FROM galgame WHERE " + r.where).Scan(&n).Error; err != nil {
			return err
		}
		slog.Info("pass1", "rule", r.label, "rows_to_set", n)
		if apply && n > 0 {
			if err := g.Exec("UPDATE galgame SET release_precision = ? WHERE "+r.where, r.set).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// pass2VNDB re-derives true precision for the only ambiguous set: vndb-sourced
// rows whose release_date is the 1st of a month. Exact-1st results stay 'day'
// (set by pass 1); month/year results are corrected.
func pass2VNDB(ctx context.Context, g *gorm.DB, vc *vndb.Client, apply bool) error {
	type row struct {
		ID     int
		VNDBID string `gorm:"column:vndb_id"`
	}
	var rows []row
	if err := g.Raw(`
		SELECT id, vndb_id FROM galgame
		WHERE vndb_id <> '' AND release_date IS NOT NULL
		  AND EXTRACT(DAY FROM release_date) = 1
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		return err
	}
	slog.Info("pass2 candidates (1st-of-month, vndb-sourced)", "rows", len(rows))

	const batchSize = 100
	corrected := 0
	for i := 0; i < len(rows); i += batchSize {
		end := min(i+batchSize, len(rows))
		batch := rows[i:end]

		ids := make([]string, 0, len(batch))
		byVNDB := make(map[string][]int, len(batch))
		for _, r := range batch {
			if _, seen := byVNDB[r.VNDBID]; !seen {
				ids = append(ids, r.VNDBID)
			}
			byVNDB[r.VNDBID] = append(byVNDB[r.VNDBID], r.ID)
		}

		released, err := vc.FetchVNReleasedBatch(ctx, ids)
		if err != nil {
			return err
		}
		for vid, gids := range byVNDB {
			d, prec := model.ParseLegacyReleased(released[vid])
			// Only month/year are corrections; 'day' (exact 1st) / tba / unknown
			// leave pass-1's provisional 'day' in place.
			if prec != model.PrecisionMonth && prec != model.PrecisionYear {
				continue
			}
			for _, id := range gids {
				if apply {
					if err := g.Exec(
						"UPDATE galgame SET release_date = ?, release_precision = ?, release_date_tba = false WHERE id = ?",
						d, string(prec), id,
					).Error; err != nil {
						return err
					}
				}
				corrected++
			}
		}
		slog.Info("pass2 progress", "done", end, "total", len(rows), "corrected_so_far", corrected)
	}
	slog.Info("pass2 done", "corrected", corrected, "apply", apply)
	return nil
}
