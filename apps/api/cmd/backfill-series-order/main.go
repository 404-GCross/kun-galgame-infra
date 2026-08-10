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

	_ = godotenv.Load("apps/api/.env")

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
