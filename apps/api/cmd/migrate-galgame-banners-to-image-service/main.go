// migrate-galgame-banners-to-image-service —— 将 galgame_wiki 库里历史
// `galgame.banner` URL 走 image_service 一遍，写回 `banner_image_hash`。
//
// 这是一次性脚本。幂等 + 可中断 + 死链跳过。
//
// 用法:
//   go run ./cmd/migrate-galgame-banners-to-image-service \
//       --client-id=galgame-wiki \
//       --client-secret=$KUN_GALGAME_IMAGE_SECRET \
//       --batch=100 --rps=20 --dry-run     # 先空跑确认
//
//   go run ./cmd/migrate-galgame-banners-to-image-service \
//       --client-id=galgame-wiki \
//       --client-secret=$KUN_GALGAME_IMAGE_SECRET
//
// 设计参考 docs/image_service/04-migration-plan.md 阶段 2 的骨架。
package main

import (
	"bytes"
	"context"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/imageclient"
	"api/pkg/logger"

	"gorm.io/gorm"
)

const (
	maxAttempts    = 3
	defaultPreset  = "galgame_banner"
	defaultTimeout = 60 * time.Second
)

func main() {
	var (
		baseURL      = flag.String("image-base-url", "", "image_service 基地址；默认从 cfg.ImageService.{Host,Port} 拼")
		cdnBase      = flag.String("cdn-base", "", "image_service 的 CDN base；默认从 cfg.ImageService.CDNBase 取")
		clientID     = flag.String("client-id", "", "OAuth client_id [必填]")
		clientSecret = flag.String("client-secret", "", "OAuth client_secret [必填]")
		batch        = flag.Int("batch", 100, "DB SELECT 一批多少行")
		rps          = flag.Int("rps", 20, "上传速率限制（次/秒）")
		dryRun       = flag.Bool("dry-run", false, "只扫描 + 拉老 URL，不调用 image_service / 不改 DB")
		limit        = flag.Int("limit", 0, "最多处理多少行（0=全量）；调试时用 100 先跑")
	)
	flag.Parse()

	if *clientID == "" || *clientSecret == "" {
		fmt.Fprintln(os.Stderr, "--client-id 和 --client-secret 必填")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	resolvedBase := *baseURL
	if resolvedBase == "" {
		resolvedBase = fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
	}
	resolvedCDN := *cdnBase
	if resolvedCDN == "" {
		resolvedCDN = cfg.ImageService.CDNBase
	}

	cli := imageclient.New(imageclient.Config{
		BaseURL:      resolvedBase,
		CDNBase:      resolvedCDN,
		ClientID:     *clientID,
		ClientSecret: *clientSecret,
		Timeout:      defaultTimeout,
	})

	// Sanity check: ping /healthz before grinding through the DB.
	hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hcancel()
	if err := cli.Health(hctx); err != nil {
		slog.Error("image_service unhealthy or unreachable", "base_url", resolvedBase, "err", err)
		os.Exit(1)
	}
	slog.Info("image_service reachable", "base_url", resolvedBase)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	httpClient := &http.Client{Timeout: defaultTimeout}
	ctx := context.Background()
	tick := time.NewTicker(time.Second / time.Duration(maxInt(1, *rps)))
	defer tick.Stop()

	var (
		processed, succeeded, failed, skipped int64
		migratedHashes                        []string
	)
	start := time.Now()

	// Keyset pagination over `id`. Resumable: every successful row gets
	// banner_migration_status=1 (success) or =2 (permanent failure after
	// retries). PR5 retired the banner_image_hash column, so the WHERE
	// clause now relies solely on banner_migration_status — galgames
	// whose banner has already been migrated (status=1) or permanently
	// failed (status=2) are skipped.
	lastID := 0
	for {
		var rows []model.Galgame
		q := db.DB().WithContext(ctx).
			Where(`id > ? AND banner != '' AND banner_migration_status NOT IN (1, 2)`, lastID).
			Order("id ASC").
			Limit(*batch)
		if *limit > 0 {
			q = q.Limit(minInt(*batch, *limit-int(processed)))
		}
		if err := q.Find(&rows).Error; err != nil {
			slog.Error("query batch", "err", err, "last_id", lastID)
			os.Exit(1)
		}
		if len(rows) == 0 {
			break
		}

		stop := false
		for _, g := range rows {
			processed++
			lastID = g.ID
			<-tick.C // rate-limit

			outcome := processOne(ctx, &g, httpClient, cli, db.DB(), *dryRun)
			switch outcome.kind {
			case outcomeSuccess:
				succeeded++
				migratedHashes = append(migratedHashes, outcome.hash)
			case outcomeDryRun:
				skipped++
			case outcomeFail:
				failed++
				if outcome.fatal {
					slog.Error("quota exceeded; stopping",
						"processed", processed, "succeeded", succeeded, "failed", failed)
					stop = true
				}
			}

			if processed%100 == 0 {
				elapsed := time.Since(start)
				rate := float64(processed) / elapsed.Seconds()
				slog.Info("progress",
					"processed", processed,
					"succeeded", succeeded,
					"failed", failed,
					"skipped_dry_run", skipped,
					"rate_per_sec", fmt.Sprintf("%.1f", rate),
					"elapsed", elapsed.Truncate(time.Second),
				)
			}
			if *limit > 0 && processed >= int64(*limit) {
				stop = true
			}
			if stop {
				break
			}
		}
		if stop {
			break
		}
	}

	slog.Info("migration finished",
		"processed", processed,
		"succeeded", succeeded,
		"failed", failed,
		"skipped_dry_run", skipped,
		"elapsed", time.Since(start).Truncate(time.Second),
	)

	if !*dryRun && len(migratedHashes) > 0 {
		slog.Info("final reference-ping for migrated hashes", "count", len(migratedHashes))
		for _, b := range chunk(migratedHashes, 1000) {
			if _, err := cli.ReferencePing(ctx, b); err != nil {
				slog.Warn("final ping failed", "err", err)
			}
		}
	}
}

// outcome reports the per-row result back to the main loop.
type outcomeKind int

const (
	outcomeSuccess outcomeKind = iota
	outcomeDryRun
	outcomeFail
)

type outcome struct {
	kind  outcomeKind
	hash  string
	fatal bool // true means stop the whole run (e.g., quota exceeded)
}

// processOne handles fetching, uploading, and DB update for a single row.
func processOne(
	ctx context.Context,
	g *model.Galgame,
	httpClient *http.Client,
	cli *imageclient.Client,
	db *gorm.DB,
	dryRun bool,
) outcome {
	body, err := fetchOldObject(httpClient, g.Banner)
	if err != nil {
		recordFailure(db, g.ID, g.BannerMigrationAttempts, fmt.Errorf("fetch: %w", err))
		slog.Warn("fetch", "gid", g.ID, "url", g.Banner, "err", err)
		return outcome{kind: outcomeFail}
	}

	if dryRun {
		slog.Info("[dry-run]", "gid", g.ID, "url", g.Banner, "size", len(body))
		return outcome{kind: outcomeDryRun}
	}

	result, err := cli.Upload(ctx, bytes.NewReader(body), "banner.bin", defaultPreset)
	if err != nil {
		recordFailure(db, g.ID, g.BannerMigrationAttempts, fmt.Errorf("upload: %w", err))
		if stderrors.Is(err, imageclient.ErrQuotaExceeded) {
			return outcome{kind: outcomeFail, fatal: true}
		}
		slog.Warn("upload", "gid", g.ID, "err", err)
		return outcome{kind: outcomeFail}
	}

	// Insert the uploaded hash into galgame_cover as the pinned cover
	// (sort_order=0) + bump banner_migration_status so future runs skip
	// this row. PR5 retired galgame.banner_image_hash, so galgame_cover
	// is now the sole image_service reference; the migration writes to
	// it exclusively. Same transaction so partial failure can't leave
	// status flipped without the cover row.
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Galgame{}).
			Where("id = ?", g.ID).
			Updates(map[string]any{
				"banner_migration_status":   1,
				"banner_migration_attempts": g.BannerMigrationAttempts + 1,
			}).Error; err != nil {
			return err
		}
		// Idempotent: if the cover row already exists (e.g. inserted by
		// the wiki edit UI between sweeps), ON CONFLICT DO NOTHING keeps
		// the row but we still mark status=1 above so we won't retry.
		return tx.Exec(
			`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, created)
			 VALUES (?, ?, 0, 0, 0, '', '', NOW())
			 ON CONFLICT (galgame_id, image_hash) DO NOTHING`,
			g.ID, result.Hash,
		).Error
	}); err != nil {
		slog.Error("update db", "gid", g.ID, "err", err)
		return outcome{kind: outcomeFail}
	}
	return outcome{kind: outcomeSuccess, hash: result.Hash}
}

// fetchOldObject HTTP-GET 老 CDN URL，限制最大 50MB body。
func fetchOldObject(c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

// recordFailure 累加 attempts；超过 maxAttempts 标记为永久失败 (status=2)。
func recordFailure(db *gorm.DB, gid int, attempts int16, cause error) {
	newAttempts := attempts + 1
	updates := map[string]any{
		"banner_migration_attempts": newAttempts,
	}
	if int(newAttempts) >= maxAttempts {
		updates["banner_migration_status"] = 2
	}
	if err := db.Model(&model.Galgame{}).
		Where("id = ?", gid).
		Updates(updates).Error; err != nil {
		slog.Warn("record failure", "gid", gid, "err", err)
	}
	slog.Debug("recorded failure", "gid", gid, "attempts", newAttempts, "cause", cause)
}

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		end := min(i+n, len(src))
		out = append(out, src[i:end])
	}
	return out
}

func maxInt(a, b int) int { return max(a, b) }
func minInt(a, b int) int { return min(a, b) }
