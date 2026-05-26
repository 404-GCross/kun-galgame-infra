// user-avatar-refping keeps user avatar images alive in image_service
// (TTL: >365d unreferenced → soft-deleted, +30d → physical delete). Most
// users set their avatar once and never change it — without this daily
// ping their avatars vanish ~13 months later because the CDN read path
// bypasses image_service and doesn't refresh last_referenced_at.
//
// This is a thin shell. The logic lives in internal/jobs (single source
// of truth) so the in-process scheduler and this CLI run identical code.
// CLI / break-glass:
//
//	go run ./cmd/user-avatar-refping --dry-run
//	0 4 * * *  /usr/local/bin/kun-user-avatar-refping   # if scheduling externally
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

	summary, err := jobs.RunUserAvatarRefping(context.Background(), cfg, jobs.UserAvatarRefpingOpts{
		Batch:   *batch,
		Timeout: *timeout,
		DryRun:  *dryRun,
	})
	if err != nil {
		slog.Error("user-avatar-refping failed", "summary", summary, "err", err)
		os.Exit(1)
	}
	slog.Info("user-avatar-refping complete", "summary", summary)
}
