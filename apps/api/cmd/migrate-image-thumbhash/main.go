// Command migrate-image-thumbhash backfills the images.thumbhash column for
// rows uploaded before the column existed.
//
// For each image still lacking a thumbhash it fetches the stored main WebP,
// decodes it, computes the ThumbHash placeholder, and UPDATEs the row. The job
// is idempotent (it only ever selects rows with an empty thumbhash) and fully
// re-runnable — safe to stop and resume. It is heavy: one S3 GET + decode per
// image, so it is bounded by --concurrency and logs progress per page.
//
// Runs against the images DB (kun_images) + its S3 bucket. width/height already
// exist for every row (computed at upload), so the galgame detail/list pages
// get correct aspect ratios immediately on deploy; this job only lights up the
// blur-up placeholder as it fills thumbhash.
//
//	go run ./cmd/migrate-image-thumbhash                        # dry-run (counts only)
//	go run ./cmd/migrate-image-thumbhash --apply                # actually write
//	go run ./cmd/migrate-image-thumbhash --apply --concurrency 16 --limit 5000
package main

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"api/internal/infrastructure/database"
	imgModel "api/internal/platform/image/model"
	"api/internal/platform/image/processor"
	"api/internal/platform/image/storage"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "actually write thumbhash (default: dry-run, count only)")
	batch := flag.Int("batch", 500, "rows fetched per page")
	concurrency := flag.Int("concurrency", 8, "parallel decode/compute workers")
	limit := flag.Int("limit", 0, "stop after scheduling N rows (0 = all)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	pdb, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("images db connect", "err", err)
		os.Exit(1)
	}
	defer pdb.Close()
	db := pdb.DB()

	// Self-sufficient: ensure the column exists so this can run even before the
	// image service has restarted with the new model (AutoMigrate is additive).
	if err := db.AutoMigrate(&imgModel.Image{}); err != nil {
		slog.Error("automigrate", "err", err)
		os.Exit(1)
	}

	s3, err := storage.NewClient(cfg.ImageS3)
	if err != nil {
		slog.Error("s3 init", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	var updated, skipped, failed atomic.Int64
	scanned := 0
	lastID := int64(0)
	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	start := time.Now()

	type row struct {
		ID         int64
		Hash       string
		StorageKey string
	}

outer:
	for {
		var rows []row
		if err := db.WithContext(ctx).
			Model(&imgModel.Image{}).
			Select("id", "hash", "storage_key").
			Where("thumbhash = '' AND deleted_at IS NULL AND id > ?", lastID).
			Order("id ASC").
			Limit(*batch).
			Scan(&rows).Error; err != nil {
			slog.Error("scan page", "err", err, "after_id", lastID)
			break
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			lastID = r.ID
			if *limit > 0 && scanned >= *limit {
				break outer
			}
			scanned++
			wg.Add(1)
			sem <- struct{}{}
			go func(r row) {
				defer wg.Done()
				defer func() { <-sem }()
				th, err := computeThumbhash(ctx, s3, r.StorageKey)
				if err != nil {
					failed.Add(1)
					slog.Warn("compute failed", "hash", r.Hash, "key", r.StorageKey, "err", err)
					return
				}
				if th == "" {
					skipped.Add(1)
					return
				}
				if !*apply {
					updated.Add(1) // would-update count in dry-run
					return
				}
				if err := db.WithContext(ctx).
					Model(&imgModel.Image{}).
					Where("hash = ?", r.Hash).
					Update("thumbhash", th).Error; err != nil {
					failed.Add(1)
					slog.Warn("update failed", "hash", r.Hash, "err", err)
					return
				}
				updated.Add(1)
			}(r)
		}
		slog.Info("progress",
			"scanned", scanned, "updated", updated.Load(),
			"failed", failed.Load(), "after_id", lastID)
	}
	wg.Wait()

	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLIED"
	}
	slog.Info("thumbhash backfill complete",
		"mode", mode,
		"scanned", scanned,
		"updated", updated.Load(),
		"skipped", skipped.Load(),
		"failed", failed.Load(),
		"elapsed", time.Since(start).String(),
	)
}

// computeThumbhash fetches a stored image, decodes it, and returns its base64
// ThumbHash. Uses the same processor path as the upload pipeline so the result
// is byte-identical to what a fresh upload would have produced.
func computeThumbhash(ctx context.Context, s3 *storage.Client, key string) (string, error) {
	rc, err := s3.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	img, _, err := processor.DecodeFromBytes(b)
	if err != nil {
		return "", err
	}
	return processor.Thumbhash(img), nil
}
