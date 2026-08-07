// build-derived-series materializes series the catalog already implied but
// never stored: connected components over the series-ish work relation edges
// ({sequel_of, side_story_of, fandisc_of, same_series}), written under source
// `derived` (18) with external_id "comp:<smallest member work id>". Logic lives
// in internal/jobs/derivedseries.
//
// It is a REAPER, not a seeder — run it as often as the relation import grows
// the graph. Members are inserted-absent and deleted-stale, a series that falls
// below 2 members is deleted whole, display_name is recomputed every pass (the
// builder is this lane's only writer), and a run over an unchanged graph writes
// nothing and touches nothing.
//
// It never touches the dlsite or curated lanes: a component that overlaps an
// existing series there is refused and written to the worklist instead, along
// with any component still 30+ works after the strong-edge split. Those two
// files are the human's half of the wave.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted.
//
//	# dry-run: bucket counts + the worklist, nothing written
//	go run ./cmd/build-derived-series --worklist /tmp/derived-worklist.jsonl \
//	    --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog sslmode=disable"
//
//	# the real build, with receipts
//	go run ./cmd/build-derived-series --apply \
//	    --receipts /tmp/derived-series.jsonl --worklist /tmp/derived-worklist.jsonl \
//	    --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog sslmode=disable"
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/derivedseries"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	receipts := flag.String("receipts", "", "path to a jsonl receipt log (one object per built series)")
	worklist := flag.String("worklist", "", "path to a jsonl log of the components this run refused to build")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives logging only; the catalog DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := derivedseries.Run(context.Background(), derivedseries.Opts{
		Apply: *apply, DSN: *dsn, Receipts: *receipts, Worklist: *worklist,
	})
	if err != nil {
		slog.Error("build-derived-series", "error", err)
		os.Exit(1)
	}
	slog.Info("build-derived-series done", "apply", *apply,
		"works", st.Works, "edges", st.Edges, "components", st.Components,
		"skipped_overlap", st.SkippedOverlap, "skipped_giant", st.SkippedGiant,
		"skipped_no_name", st.SkippedNoName,
		"giants_split", st.GiantsSplit, "eligible", st.Eligible, "members_wanted", st.MembersWanted,
		"series_created", st.SeriesCreated, "series_renamed", st.SeriesRenamed,
		"series_deleted", st.SeriesDeleted, "members_added", st.MembersAdded,
		"members_stale", st.MembersStale, "order_changed", st.OrderChanged,
		"touched_works", st.TouchedWorks,
		"named_by_prefix", st.NamedByPrefix, "named_by_fallback", st.NamedByFallback)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
