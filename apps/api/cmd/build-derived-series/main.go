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

	_ = godotenv.Load("apps/api/.env")

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
		"bridges", st.Bridges, "bridge_edges_cut", st.BridgeEdgesCut,
		"giants_split", st.GiantsSplit, "eligible", st.Eligible, "members_wanted", st.MembersWanted,
		"series_created", st.SeriesCreated, "series_renamed", st.SeriesRenamed,
		"series_deleted", st.SeriesDeleted, "members_added", st.MembersAdded,
		"members_stale", st.MembersStale, "order_changed", st.OrderChanged,
		"touched_works", st.TouchedWorks,
		"named_by_prefix", st.NamedByPrefix, "named_by_fallback", st.NamedByFallback,
		"named_by_override", st.NamedByOverride, "overrides_stale", st.OverridesStale)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
