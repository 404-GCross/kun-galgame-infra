// seed-curated-series restores the galgame wiki's hand-curated series onto the
// catalog's curated lane (wave 180). It reads the two frozen artifacts in
// refs/proj/180-artifacts — wiki-series.tsv and wiki-series-members.tsv, the
// only surviving copy of that grouping after the wave-161 DROP — and writes
// catalog_series (source=curated, external_id="wiki:<id>"),
// catalog_series_member and catalog_series_intro (zh-Hans). Logic lives in
// internal/jobs/curatedseries.
//
// RUN IT ONCE. Every write is ON CONFLICT DO NOTHING and nothing is ever
// UPDATEd, because after this seed the human edit face is the curated lane's
// only writer: it full-replaces a work's curated memberships and lets a curator
// rename a curated series. A second --apply pass is therefore a zero-write, and
// that is the acceptance proof — not a licence to rerun it as a sync job.
//
// A wiki series whose members are already grouped by a single dlsite series is
// skipped (expected: 2). Nothing is ever attached to a dlsite series — the
// dlsite series importer reaps foreign members.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted.
//
//	# dry-run: counters only
//	go run ./cmd/seed-curated-series \
//	    --artifacts refs/proj/180-artifacts \
//	    --dsn "host=localhost port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# the real seed, with a receipt log
//	go run ./cmd/seed-curated-series --apply \
//	    --artifacts refs/proj/180-artifacts --receipts /tmp/curated-series.jsonl \
//	    --dsn "host=localhost port=5432 user=postgres password=... dbname=kun_catalog sslmode=disable"
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

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives logging only; the catalog DB is reached exclusively via --dsn.
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
