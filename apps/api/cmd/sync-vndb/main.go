// sync-vndb syncs VNDB visual novels into the galgame wiki DB as
// status=2 drafts (incremental by default, --full to reseed from v0).
//
// Thin shell: logic lives in internal/jobs/vndbsync (single source of
// truth) so the in-process scheduler and this CLI run identical code.
// CLI / break-glass:
//
//	go run ./cmd/sync-vndb                 # incremental
//	go run ./cmd/sync-vndb --full          # full reseed
//	go run ./cmd/sync-vndb --tagmap=PATH   # override tagMap.ts path
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	full := flag.Bool("full", false, "Full sync from v0 (default: incremental)")
	// Preserve the original CLI default (relative to cmd/sync-vndb dir).
	tagMapPath := flag.String("tagmap", "../../docs/tagMap.ts", "Path to tagMap.ts")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	summary, err := jobs.RunSyncVNDB(context.Background(), cfg, jobs.SyncVNDBOpts{
		Full:       *full,
		TagMapPath: *tagMapPath,
	})
	if err != nil {
		slog.Error("sync-vndb failed", "err", err)
		os.Exit(1)
	}
	slog.Info("sync-vndb complete", "summary", summary)
}
