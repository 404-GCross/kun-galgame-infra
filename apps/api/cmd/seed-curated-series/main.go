package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/curatedseries"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	artifacts := flag.String("artifacts", "", "directory holding wiki-series.tsv and wiki-series-members.tsv — REQUIRED")
	receipts := flag.String("receipts", "", "path to a jsonl receipt log (one object per decided series; empty = no log)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := curatedseries.Run(context.Background(), curatedseries.Opts{
		Apply:        *apply,
		DSN:          *dsn,
		ArtifactsDir: *artifacts,
		Receipts:     *receipts,
	})
	if err != nil {
		slog.Error("seed-curated-series", "error", err)
		os.Exit(1)
	}
	slog.Info("seed-curated-series done", "apply", *apply,
		"series_in_file", st.SeriesInFile, "series_with_members", st.SeriesWithMembers,
		"series_seeded", st.SeriesSeeded, "series_skipped_covered", st.SeriesSkippedCovered,
		"series_existing", st.SeriesExisting,
		"members_inserted", st.MembersInserted, "members_existing", st.MembersExisting,
		"members_skipped_no_work", st.MembersSkippedNoWork,
		"intros_inserted", st.IntrosInserted, "intros_existing", st.IntrosExisting,
		"touched_works", st.TouchedWorks)
}
