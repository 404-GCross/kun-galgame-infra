// backfill-series-order gives every pre-wave-184 series membership row its
// position and kind (catalog_series_member.position / .kind). Both columns
// landed with a DEFAULT 0 sentinel, so until this runs the read face sorts
// every existing series by work id and reports every member's role as unknown.
//
// Scope: the dlsite and curated lanes — the two that predate the columns. The
// derived lane (cmd/build-derived-series) assigns the facets itself as it
// materializes a component and is deliberately out of scope here.
//
// It is a RECONCILE, not a one-shot: the ordering is a pure function of the
// members' release dates and the relation edges among them, so re-running it
// after a date correction is the supported way to fix an order, and a run over
// unchanged data writes nothing and touches nothing. Logic lives in
// internal/jobs/seriesorder.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted.
//
//	# dry-run: how many member rows would move
//	go run ./cmd/backfill-series-order \
//	    --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog sslmode=disable"
//
//	# the real backfill, with a receipt log
//	go run ./cmd/backfill-series-order --apply --receipts /tmp/series-order.jsonl \
//	    --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog sslmode=disable"
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/seriesorder"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	receipts := flag.String("receipts", "", "path to a jsonl receipt log (one object per series; empty = no log)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives logging only; the catalog DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := seriesorder.Backfill(context.Background(), seriesorder.BackfillOpts{
		Apply: *apply, DSN: *dsn, Receipts: *receipts,
	})
	if err != nil {
		slog.Error("backfill-series-order", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-series-order done", "apply", *apply,
		"series", st.Series, "series_with_order", st.SeriesWithOrder,
		"members", st.Members, "members_changed", st.MembersChanged,
		"touched_works", st.TouchedWorks)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
