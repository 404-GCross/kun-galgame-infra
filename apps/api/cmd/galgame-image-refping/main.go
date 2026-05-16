// galgame-image-refping keeps galgame wiki banner images alive in
// image_service (TTL: >365d unreferenced → soft-deleted, +30d → physical
// delete). Set-once banners only get the upload-time TTL touch otherwise
// and vanish ~13 months later. Mandated per-caller daily ping
// (docs/image_service/04-migration-plan.md, 06-integration-guide.md §七/§九).
//
// This is a thin shell. The logic lives in internal/jobs (single source
// of truth) so the in-process scheduler and this CLI run identical code.
// CLI / break-glass:
//
//	go run ./cmd/galgame-image-refping --dry-run
//	0 4 * * *  /usr/local/bin/kun-galgame-image-refping   # if scheduling externally
//
// Flags:
//
//	--batch=1000    hashes per reference-ping request (max 1000)
//	--timeout=30m   overall run timeout
//	--dry-run       collect + log the hash count, do not call image_service
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
	batch := flag.Int("batch", 1000, "hashes per reference-ping request (max 1000)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall run timeout")
	dryRun := flag.Bool("dry-run", false, "log only, do not call image_service")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	summary, err := jobs.RunGalgameImageRefping(context.Background(), cfg, jobs.GalgameImageRefpingOpts{
		Batch:   *batch,
		Timeout: *timeout,
		DryRun:  *dryRun,
	})
	if err != nil {
		slog.Error("galgame-image-refping failed", "summary", summary, "err", err)
		os.Exit(1)
	}
	slog.Info("galgame-image-refping complete", "summary", summary)
}
