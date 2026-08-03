// image-ref-audit reconciles catalog's image REFERENCES against the bytes
// image_service still holds. A reference whose bytes were deleted renders as an
// empty frame in the gallery, and thirty days after the soft-delete the GC makes
// the loss permanent — so the value of this sweep is entirely in catching a
// deletion while it is still inside that window.
//
// This is a thin shell. The logic lives in internal/jobs (single source of
// truth) so the in-process scheduler and this CLI run identical code.
//
//	go run ./cmd/image-ref-audit
//
// One difference from the scheduled run: the CLI writes no job_run row, so it
// reads the alerting baseline but never updates it. Use it to LOOK; the daily
// scheduled run is what maintains the known-broken set that suppresses repeat
// alerts.
//
// Exit code 1 means newly-broken references were found (or the audit could not
// complete) — the summary is logged either way.
//
// Flags:
//
//	--batch=1000    hashes per meta-batch probe (max 1000)
//	--timeout=30m   overall run timeout
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
	batch := flag.Int("batch", 1000, "hashes per meta-batch probe (max 1000)")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall run timeout")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	summary, err := jobs.RunImageRefAudit(context.Background(), cfg, jobs.ImageRefAuditOpts{
		Batch:   *batch,
		Timeout: *timeout,
	})
	if err != nil {
		slog.Error("image-ref-audit failed", "summary", summary, "err", err)
		os.Exit(1)
	}
	slog.Info("image-ref-audit complete", "summary", summary)
}
