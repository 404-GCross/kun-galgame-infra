// migrate-images is the platform-side migration script. It scans an old
// S3-compatible bucket for avatar / galgame-banner objects, re-processes
// them through the image service pipeline, and populates the new
// `images` + `image_site_usage` tables.
//
// topic-image objects are intentionally NOT migrated — per the design doc
// (docs/image_service/04-migration-plan.md), topic markdown URLs are kept
// read-only in their old bucket forever.
//
// Usage:
//   migrate-images --site=kungal --type=avatar --preset=avatar \
//       --old-endpoint=https://s3.example.com --old-bucket=kungal-prod \
//       --old-access-key=... --old-secret=... \
//       --prefix=avatar/ --workers=10 --rps=100 --dry-run
//
// Output: prints JSON summary on exit; per-object progress to stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/image/model"
	"api/internal/platform/image/preset"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/service"
	"api/internal/platform/image/storage"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func main() {
	var (
		site         = flag.String("site", "", "site key (kungal/moyu/galgame_wiki) [required]")
		entityType   = flag.String("type", "", "entity type (avatar/banner) [required]")
		presetName   = flag.String("preset", "", "preset to apply (avatar / galgame_banner) [required]")
		prefix       = flag.String("prefix", "", "old bucket key prefix [required]")
		oldEndpoint  = flag.String("old-endpoint", "", "old S3 endpoint")
		oldRegion    = flag.String("old-region", "auto", "old S3 region")
		oldBucket    = flag.String("old-bucket", "", "old bucket name")
		oldAccessKey = flag.String("old-access-key", "", "old access key")
		oldSecret    = flag.String("old-secret", "", "old secret")
		oldPathStyle = flag.Bool("old-path-style", false, "use path-style addressing")
		workers      = flag.Int("workers", 10, "concurrent workers")
		rps          = flag.Int("rps", 100, "max requests per second")
		dryRun       = flag.Bool("dry-run", false, "scan only; no writes")
		resumeOnly   = flag.Bool("resume", false, "only re-process non-copied rows")
	)
	flag.Parse()

	if *site == "" || *entityType == "" || *presetName == "" || *prefix == "" {
		fmt.Fprintln(os.Stderr, "--site, --type, --preset, and --prefix are required")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	// Target (new) infrastructure.
	db, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("images db", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.AutoMigrate(&model.Image{}, &model.ImageSiteUsage{}, &model.MigrationProgress{}); err != nil {
		slog.Error("automigrate", "err", err)
		os.Exit(1)
	}

	newS3, err := storage.NewClient(cfg.ImageS3)
	if err != nil {
		slog.Error("new s3", "err", err)
		os.Exit(1)
	}
	if err := newS3.EnsureBucket(context.Background()); err != nil {
		slog.Warn("ensure new bucket (non-fatal if preexisting)", "err", err)
	}

	// Source (old) bucket.
	oldS3, err := buildOldS3Client(*oldEndpoint, *oldRegion, *oldAccessKey, *oldSecret, *oldPathStyle)
	if err != nil {
		slog.Error("old s3", "err", err)
		os.Exit(1)
	}

	// Presets + service.
	presets, err := preset.Load(cfg.ImageService.PresetsPath)
	if err != nil {
		slog.Error("presets", "err", err, "path", cfg.ImageService.PresetsPath)
		os.Exit(1)
	}
	imgRepo := repository.NewImageRepository(db.DB())
	usageRepo := repository.NewSiteUsageRepository(db.DB())
	svc := service.New(presets, newS3, imgRepo, usageRepo, cfg.ImageService.CDNBase)

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	m := &migrator{
		db:         db.DB(),
		oldS3:      oldS3,
		oldBucket:  *oldBucket,
		site:       *site,
		entityType: *entityType,
		presetName: *presetName,
		svc:        svc,
		dryRun:     *dryRun,
		resumeOnly: *resumeOnly,
		rate:       time.Second / time.Duration(maxInt(1, *rps)),
		workers:    maxInt(1, *workers),
	}

	summary, err := m.run(ctx, *prefix)
	if err != nil {
		slog.Error("migration failed", "err", err)
	}
	b, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Println(string(b))
}

// ---- migrator state ----

type migrator struct {
	db         *gorm.DB
	oldS3      *s3.Client
	oldBucket  string
	site       string
	entityType string
	presetName string
	svc        *service.Service
	dryRun     bool
	resumeOnly bool
	rate       time.Duration
	workers    int

	scanned, copied, skipped, failed atomic.Int64
}

type migratorSummary struct {
	Site       string `json:"site"`
	EntityType string `json:"entity_type"`
	Scanned    int64  `json:"scanned"`
	Copied     int64  `json:"copied"`
	Skipped    int64  `json:"skipped"`
	Failed     int64  `json:"failed"`
	Duration   string `json:"duration"`
	DryRun     bool   `json:"dry_run"`
}

func (m *migrator) run(ctx context.Context, prefix string) (*migratorSummary, error) {
	start := time.Now()

	jobs := make(chan string, m.workers*2)
	ticker := time.NewTicker(m.rate)
	defer ticker.Stop()

	var wg sync.WaitGroup
	for range m.workers {
		wg.Go(func() {
			for key := range jobs {
				if err := m.processKey(ctx, key); err != nil {
					m.failed.Add(1)
					slog.Warn("process key failed", "key", key, "err", err)
				}
			}
		})
	}

	var continuationToken *string
	for {
		if ctx.Err() != nil {
			break
		}
		out, err := m.oldS3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(m.oldBucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			close(jobs)
			wg.Wait()
			return nil, fmt.Errorf("list old bucket: %w", err)
		}
		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			if key == "" || strings.HasSuffix(key, "/") {
				continue
			}
			m.scanned.Add(1)
			select {
			case <-ctx.Done():
			case <-ticker.C:
				jobs <- key
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}
	close(jobs)
	wg.Wait()

	return &migratorSummary{
		Site:       m.site,
		EntityType: m.entityType,
		Scanned:    m.scanned.Load(),
		Copied:     m.copied.Load(),
		Skipped:    m.skipped.Load(),
		Failed:     m.failed.Load(),
		Duration:   time.Since(start).String(),
		DryRun:     m.dryRun,
	}, nil
}

func (m *migrator) processKey(ctx context.Context, key string) error {
	// Resume: skip keys already copied.
	if m.resumeOnly {
		var existing model.MigrationProgress
		err := m.db.WithContext(ctx).
			Where("site = ? AND old_key = ?", m.site, key).
			First(&existing).Error
		if err == nil && existing.Status == "copied" {
			m.skipped.Add(1)
			return nil
		}
	}

	obj, err := m.oldS3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(m.oldBucket),
		Key:    aws.String(key),
	})
	if err != nil {
		m.recordFailure(ctx, key, err)
		return err
	}
	body, err := io.ReadAll(obj.Body)
	_ = obj.Body.Close()
	if err != nil {
		m.recordFailure(ctx, key, err)
		return err
	}

	if m.dryRun {
		fmt.Printf("[dry-run] %s -> would process (%d bytes)\n", key, len(body))
		m.copied.Add(1)
		return nil
	}

	result, err := m.svc.Upload(ctx, service.UploadRequest{
		Body:           body,
		Preset:         m.presetName,
		Site:           m.site,
		UploaderSub:    "",
		UploaderClient: "migrate-images",
		UploaderIP:     "",
	})
	if err != nil {
		m.recordFailure(ctx, key, err)
		return err
	}

	m.copied.Add(1)
	m.recordSuccess(ctx, key, result.Hash)
	fmt.Printf("%s -> %s (dedup=%v)\n", key, result.Hash, result.Deduplicated)
	return nil
}

// recordSuccess upserts a MigrationProgress row with status=copied.
func (m *migrator) recordSuccess(ctx context.Context, oldKey, hash string) {
	now := time.Now()
	row := &model.MigrationProgress{
		Site:       m.site,
		EntityType: m.entityType,
		OldKey:     oldKey,
		Hash:       hash,
		Status:     "copied",
		MigratedAt: &now,
	}
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site"}, {Name: "old_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"hash":        hash,
			"status":      "copied",
			"migrated_at": now,
			"error_msg":   "",
		}),
	}).Create(row).Error; err != nil {
		slog.Warn("record success failed", "key", oldKey, "err", err)
	}
}

// recordFailure upserts a MigrationProgress row with status=failed.
func (m *migrator) recordFailure(ctx context.Context, oldKey string, cause error) {
	row := &model.MigrationProgress{
		Site:       m.site,
		EntityType: m.entityType,
		OldKey:     oldKey,
		Status:     "failed",
		ErrorMsg:   truncate(cause.Error(), 500),
	}
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site"}, {Name: "old_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status":    "failed",
			"error_msg": row.ErrorMsg,
		}),
	}).Create(row).Error; err != nil {
		slog.Warn("record failure failed", "key", oldKey, "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func buildOldS3Client(endpoint, region, accessKey, secret string, pathStyle bool) (*s3.Client, error) {
	if accessKey == "" || secret == "" {
		return nil, fmt.Errorf("old-access-key and old-secret are required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secret, "")),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = pathStyle
	}), nil
}
