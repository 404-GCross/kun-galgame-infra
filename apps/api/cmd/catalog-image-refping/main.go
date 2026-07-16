// catalog-image-refping keeps catalog CHARACTER portraits alive in the image
// service (TTL: >365d unreferenced → soft-deleted, +30d → physical delete).
// Portraits are set-once (step-48 backfill) so without this daily ping they only
// ever get the single upload-time TTL touch and vanish ~13 months later.
//
// This is a thin shell. The logic lives in internal/jobs (single source of
// truth) so the in-process scheduler and this CLI run identical code. The job
// authenticates as the catalog image client (site_key "catalog") — reference-ping
// is site-scoped, so any other identity 404s every hash and portraits rot.
//
//	go run ./cmd/catalog-image-refping --dry-run
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

	summary, err := jobs.RunCatalogImageRefping(context.Background(), cfg, jobs.CatalogImageRefpingOpts{
		Batch:   *batch,
		Timeout: *timeout,
		DryRun:  *dryRun,
	})
	if err != nil {
		slog.Error("catalog-image-refping failed", "summary", summary, "err", err)
		os.Exit(1)
	}
	slog.Info("catalog-image-refping complete", "summary", summary)
}
