// sync-vndb-scores ingests each vndb-anchored galgame's VNDB rating + votecount
// into the galgame_vndb_meta narrow table, refreshed idempotently against the
// current VNDB truth. A zero-vote VN stores a NULL rating (never a fake zero);
// a VN VNDB no longer returns writes no row.
//
// Thin shell: logic lives in internal/jobs/vndbscores (single source of truth)
// so this CLI and the scheduled "sync-vndb-scores" job run identical code. The
// job runs daily over every vndb game (a few hundred cheap /vn calls); use this
// CLI for a sampled trial (--limit) or an ad-hoc full refresh. Applies by
// default (scores are safe, additive metadata); pass --dry-run to preview.
//
//	go run ./cmd/sync-vndb-scores --limit 200   # sampled trial (writes)
//	go run ./cmd/sync-vndb-scores               # full refresh
//	go run ./cmd/sync-vndb-scores --dry-run     # fetch + count, no writes
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/jobs"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "fetch + count only, no writes (default: apply)")
	limit := flag.Int("limit", 0, "max galgames to process (0 = all)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if _, err := jobs.RunSyncVNDBScores(context.Background(), cfg, jobs.SyncVNDBScoresOpts{
		Apply: !*dryRun,
		Gap:   *gap,
		Limit: *limit,
	}); err != nil {
		slog.Error("sync-vndb-scores", "error", err)
		os.Exit(1)
	}
}
