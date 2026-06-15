// migrate-galgame-banners-to-image-service —— 将 galgame_wiki 库里历史
// `galgame.banner` URL 走 image_service 一遍，写回 `banner_image_hash`。
//
// 这是一次性脚本。幂等 + 可中断 + 死链跳过。
//
// 用法:
//
//	go run ./cmd/migrate-galgame-banners-to-image-service \
//	    --client-id=galgame-wiki \
//	    --client-secret=$KUN_GALGAME_IMAGE_SECRET \
//	    --batch=100 --rps=20 --dry-run     # 先空跑确认
//
//	go run ./cmd/migrate-galgame-banners-to-image-service \
//	    --client-id=galgame-wiki \
//	    --client-secret=$KUN_GALGAME_IMAGE_SECRET
//
// 设计参考 docs/image_service/04-migration-plan.md 阶段 2 的骨架。
package main

import (
	"bytes"
	"context"
	stderrors "errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	// Decoders for image.Decode (side-effect). webp/png/jpeg pass through to
	// image_service; gif/other get transcoded to PNG.
	_ "image/gif"
	_ "image/jpeg"

	"github.com/gen2brain/avif" // pure-Go AVIF decoder (wazero); moyu banners are AVIF

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
		vndbImageDir = flag.String("vndb-image-dir", "", "本地 VNDB 图片 dump 目录（rsync rsync://dl.vndb.org/vndb-img/）。t.vndb.org 的 banner 从这里读，避开外站限流；本地缺失再回退 HTTP")
		concurrency  = flag.Int("concurrency", 1, "并发 worker 数（每个 worker 串行 fetch→upload→DB）；默认 1=串行。本地 dump + 我方 image_service 可调高到 12~16")
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
		fatal                                 int32 // set when quota exceeded → stop launching
		migratedHashes                        []string
		hashMu                                sync.Mutex
	)
	start := time.Now()

	// Keyset pagination over `id` (serial page fetch), each page processed by a
	// pool of `concurrency` workers. Resumable: every successful row gets
	// banner_migration_status=1 (success) or =2 (permanent failure after
	// retries); rows already 1/2 are skipped by the WHERE. The rps ticker gates
	// the *launch* rate; concurrency gates the in-flight count.
	sem := make(chan struct{}, maxInt(1, *concurrency))
	var wg sync.WaitGroup
	lastID := 0
	for atomic.LoadInt32(&fatal) == 0 {
		var rows []model.Galgame
		q := db.DB().WithContext(ctx).
			Where(`id > ? AND banner != '' AND banner_migration_status NOT IN (1, 2)`, lastID).
			Order("id ASC").
			Limit(*batch)
		if *limit > 0 {
			remaining := *limit - int(atomic.LoadInt64(&processed))
			if remaining <= 0 {
				break
			}
			q = q.Limit(minInt(*batch, remaining))
		}
		if err := q.Find(&rows).Error; err != nil {
			slog.Error("query batch", "err", err, "last_id", lastID)
			os.Exit(1)
		}
		if len(rows) == 0 {
			break
		}
		lastID = rows[len(rows)-1].ID // rows are id-ascending

		for i := range rows {
			if atomic.LoadInt32(&fatal) != 0 {
				break
			}
			if *limit > 0 && atomic.LoadInt64(&processed) >= int64(*limit) {
				break
			}
			<-tick.C // rate-limit the launch
			wg.Add(1)
			sem <- struct{}{}
			go func(g model.Galgame) {
				defer wg.Done()
				defer func() { <-sem }()
				n := atomic.AddInt64(&processed, 1)
				switch oc := processOne(ctx, &g, httpClient, cli, db.DB(), *dryRun, *vndbImageDir); oc.kind {
				case outcomeSuccess:
					atomic.AddInt64(&succeeded, 1)
					hashMu.Lock()
					migratedHashes = append(migratedHashes, oc.hash)
					hashMu.Unlock()
				case outcomeDryRun:
					atomic.AddInt64(&skipped, 1)
				case outcomeFail:
					atomic.AddInt64(&failed, 1)
					if oc.fatal {
						atomic.StoreInt32(&fatal, 1)
					}
				}
				if n%200 == 0 {
					elapsed := time.Since(start)
					slog.Info("progress",
						"processed", n,
						"succeeded", atomic.LoadInt64(&succeeded),
						"failed", atomic.LoadInt64(&failed),
						"skipped_dry_run", atomic.LoadInt64(&skipped),
						"rate_per_sec", fmt.Sprintf("%.1f", float64(n)/elapsed.Seconds()),
						"elapsed", elapsed.Truncate(time.Second),
					)
				}
			}(rows[i])
		}
		wg.Wait() // barrier per page (keeps memory bounded; lastID already advanced)
	}
	wg.Wait()
	if atomic.LoadInt32(&fatal) != 0 {
		slog.Error("quota exceeded; stopped early",
			"processed", processed, "succeeded", succeeded, "failed", failed)
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
	vndbDir string,
) outcome {
	body, err := fetchBanner(httpClient, vndbDir, g.Banner)
	if err != nil {
		recordFailure(db, g.ID, g.BannerMigrationAttempts, fmt.Errorf("fetch: %w", err))
		slog.Warn("fetch", "gid", g.ID, "url", g.Banner, "err", err)
		return outcome{kind: outcomeFail}
	}

	if dryRun {
		slog.Info("[dry-run]", "gid", g.ID, "url", g.Banner, "size", len(body))
		return outcome{kind: outcomeDryRun}
	}

	// image_service's galgame_banner preset accepts jpeg/png/webp and can't
	// decode AVIF. moyu banners are AVIF → decode to PNG here (image_service
	// re-encodes to webp). VNDB (jpg) / kungal (webp) pass straight through.
	body, err = normalizeForUpload(body)
	if err != nil {
		recordFailure(db, g.ID, g.BannerMigrationAttempts, fmt.Errorf("normalize: %w", err))
		slog.Warn("normalize", "gid", g.ID, "url", g.Banner, "err", err)
		return outcome{kind: outcomeFail}
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
		// Idempotent + don't clobber a curated cover. Target-less ON CONFLICT
		// DO NOTHING swallows BOTH unique constraints on galgame_cover: the
		// (galgame_id, image_hash) pair AND idx_galgame_cover_pinned (one
		// sort_order=0 cover per galgame, WHERE sort_order=0). So a galgame
		// that already has a pinned cover (e.g. set via the wiki edit UI)
		// keeps it — we just mark status=1 above so we won't retry. Without
		// the target-less form, those ~100 galgames violate the pinned index.
		return tx.Exec(
			`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, created)
			 VALUES (?, ?, 0, 0, 0, '', '', NOW())
			 ON CONFLICT DO NOTHING`,
			g.ID, result.Hash,
		).Error
	}); err != nil {
		slog.Error("update db", "gid", g.ID, "err", err)
		return outcome{kind: outcomeFail}
	}
	return outcome{kind: outcomeSuccess, hash: result.Hash}
}

// avifDecodeMu serializes wazero-backed AVIF decodes across workers.
var avifDecodeMu sync.Mutex

// normalizeForUpload makes bytes acceptable to image_service's galgame_banner
// preset (jpeg/png/webp). webp/png/jpeg pass through unchanged; AVIF (moyu
// banners) and anything else decodable are re-encoded to PNG. image_service
// then re-encodes everything to webp + the mini variant.
func normalizeForUpload(raw []byte) ([]byte, error) {
	switch sniffFormat(raw) {
	case "webp", "png", "jpeg":
		return raw, nil
	case "avif":
		avifDecodeMu.Lock()
		img, err := avif.Decode(bytes.NewReader(raw))
		avifDecodeMu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("avif decode: %w", err)
		}
		return encodePNG(img)
	default:
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		return encodePNG(img)
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sniffFormat identifies the image format by magic bytes (old CDNs return
// application/octet-stream). AVIF = ISO-BMFF ftyp box with the avif/avis brand.
func sniffFormat(b []byte) string {
	switch {
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "jpeg"
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "gif"
	case len(b) >= 12 && string(b[4:8]) == "ftyp" && (bytes.Contains(b[8:min(32, len(b))], []byte("avif")) || bytes.Contains(b[8:min(32, len(b))], []byte("avis"))):
		return "avif"
	default:
		return "unknown"
	}
}

const vndbImagePrefix = "https://t.vndb.org/"

// fetchBanner 取 banner 字节。设置了 vndbDir 且 URL 是 t.vndb.org 图片时，从本地
// VNDB 图片 dump（rsync 镜像）读取，避开对 t.vndb.org 的批量抓取（官方限带宽/限并发）；
// 本地缺失/为空时回退到 HTTP（极少数：dump 里没有或当天还没同步的）。其余 host（我方
// 老图床 image.kungal.com / image.moyu.moe）一律走 HTTP。
func fetchBanner(c *http.Client, vndbDir, url string) ([]byte, error) {
	if vndbDir != "" && strings.HasPrefix(url, vndbImagePrefix) {
		rel := strings.TrimPrefix(url, vndbImagePrefix) // 例如 cv/48/20348.jpg
		p := filepath.Join(vndbDir, filepath.FromSlash(rel))
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return b, nil
		}
		// 本地没有/为空 → 落到 HTTP 兜底
	}
	return fetchOldObject(c, url)
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
