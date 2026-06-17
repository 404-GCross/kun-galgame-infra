// migrate-galgame-screenshots —— one-off backfill of VNDB screenshots for
// every PUBLISHED galgame into image_service + the galgame_screenshot table.
//
// 100% OFFLINE. It reads the VNDB database dump's `vn_screenshots` (vn -> scr)
// and `images` (scr -> sexual/violence) files, and the rsync'd `vndb-img/sf/`
// image tree. There are NO VNDB API calls and NO per-image HTTP fetches — the
// dump already mirrors every byte locally, so there is no external rate-limit
// surface (which is the whole reason we pulled the dump). A t.vndb.org HTTP
// fallback exists for a file that is somehow missing locally, but it never
// fires when the rsync mirror is complete.
//
// VNDB stores at most 10 screenshots per VN, so "all screenshots" for our
// ~9.6k published games is ~61.8k images (verified against the dump).
//
// Idempotent + resumable WITHOUT a schema change: a screenshot already present
// (source='vndb', source_key=<scr>) is skipped before upload, and the INSERT is
// `ON CONFLICT DO NOTHING`, so a re-run only fills gaps. It writes
// galgame_screenshot rows ONLY — not revision snapshots. Direct edits and
// PR-merges preserve these rows (both derive current state from the live table:
// Update via loadGalgameWithRelations+TakeSnapshot, MergePR via a changedKeys
// rebase). The only path that would drop them is a Revert to a PRE-backfill
// revision (wholesale ApplySnapshot) — same property covers had before
// migrate-drop-banner-image-hash; add a companion snapshot patch later if
// revert-safety is wanted.
//
// Usage:
//
//	go run ./cmd/migrate-galgame-screenshots \
//	    --client-id=galgame-wiki-admin --client-secret=$KUN_IMAGE_CLIENT_SECRET \
//	    --vn-screenshots=./db/vn_screenshots --images=./db/images \
//	    --vndb-image-dir=./vndb-img --concurrency=8 --rps=30 --dry-run   # confirm first
//
//	go run ./cmd/migrate-galgame-screenshots \
//	    --client-id=galgame-wiki-admin --client-secret=$KUN_IMAGE_CLIENT_SECRET \
//	    --vn-screenshots=./db/vn_screenshots --images=./db/images \
//	    --vndb-image-dir=./vndb-img --concurrency=8 --rps=30
package main

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"
	"api/pkg/logger"
)

const (
	defaultPreset  = "galgame_screenshot"
	defaultTimeout = 60 * time.Second
	scrPrefix      = "sf"                  // VNDB screenshot image-id prefix
	vndbImageHost  = "https://t.vndb.org/" // HTTP fallback host
)

// rating maps a VNDB `c_*_avg` value (0-200 = average vote * 100, where the raw
// vote scale is 0=safe/tame, 1=suggestive/violent, 2=explicit/brutal) onto our
// int16 0-2 column. Round-half-up; clamp to [0,2].
func rating(avg100 int) int16 {
	return int16(min(2, max(0, (avg100+50)/100)))
}

type imgMeta struct{ sexual, violence int16 }

type galgameRow struct {
	ID     int    `gorm:"column:id"`
	VNDBID string `gorm:"column:vndb_id"`
}

func main() {
	var (
		baseURL      = flag.String("image-base-url", "", "image_service base URL; defaults to cfg.ImageService.{Host,Port}")
		cdnBase      = flag.String("cdn-base", "", "image_service CDN base; defaults to cfg.ImageService.CDNBase")
		clientID     = flag.String("client-id", "", "image OAuth client_id; defaults to the container's galgame_wiki image client if unset")
		clientSecret = flag.String("client-secret", "", "image OAuth client_secret; defaults to the container's galgame_wiki image client if unset")
		vnScrPath    = flag.String("vn-screenshots", "./db/vn_screenshots", "path to the dump's db/vn_screenshots file (id\\tscr\\trid)")
		imagesPath   = flag.String("images", "./db/images", "path to the dump's db/images file")
		vndbImageDir = flag.String("vndb-image-dir", "", "local rsync mirror of rsync://dl.vndb.org/vndb-img (must contain sf/) [required]")
		batch        = flag.Int("batch", 200, "galgame SELECT page size")
		rps          = flag.Int("rps", 30, "upload launch rate (uploads/sec)")
		concurrency  = flag.Int("concurrency", 8, "concurrent workers (one game per worker)")
		dryRun       = flag.Bool("dry-run", false, "scan + resolve bytes only; no image_service upload, no DB write")
		limit        = flag.Int("limit", 0, "max games to process (0 = all); for a small trial run")
	)
	flag.Parse()

	if *vndbImageDir == "" {
		fmt.Fprintln(os.Stderr, "--vndb-image-dir is required (the rsync mirror containing sf/)")
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	// Image client creds: flags win; otherwise fall back to the container's
	// configured image client. In the galgame container that already IS the
	// galgame_wiki client, so a prod run can use the env-file dump (KUN_IMAGE_
	// CLIENT_ID/SECRET) WITHOUT echoing the secret on the command line.
	// reference-ping is site-scoped, so this MUST authenticate as galgame_wiki.
	cid, csec := *clientID, *clientSecret
	if cid == "" || csec == "" {
		if cfg.GalgameImageClient.ClientID != "" {
			cid, csec = cfg.GalgameImageClient.ClientID, cfg.GalgameImageClient.ClientSecret
		} else {
			cid, csec = cfg.ImageClient.ClientID, cfg.ImageClient.ClientSecret
		}
	}
	if cid == "" || csec == "" {
		slog.Error("image client not configured: pass --client-id/--client-secret, or run with the galgame env-file (KUN_IMAGE_CLIENT_ID/SECRET = galgame_wiki)")
		os.Exit(2)
	}

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
		ClientID:     cid,
		ClientSecret: csec,
		Timeout:      defaultTimeout,
	})
	hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hcancel()
	if err := cli.Health(hctx); err != nil {
		slog.Error("image_service unhealthy or unreachable", "base_url", resolvedBase, "err", err)
		os.Exit(1)
	}
	slog.Info("image_service reachable", "base_url", resolvedBase)

	// Load the two dump tables into memory (vn_screenshots ~4MB, images ~14MB).
	vnMap, err := loadScreenshotMap(*vnScrPath)
	if err != nil {
		slog.Error("load vn_screenshots", "path", *vnScrPath, "err", err)
		os.Exit(1)
	}
	meta, err := loadImageMeta(*imagesPath)
	if err != nil {
		slog.Error("load images", "path", *imagesPath, "err", err)
		os.Exit(1)
	}
	slog.Info("dump loaded", "vns_with_screenshots", len(vnMap), "sf_image_meta", len(meta))

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	httpClient := &http.Client{Timeout: defaultTimeout}
	ctx := context.Background()
	tick := time.NewTicker(time.Second / time.Duration(max(1, *rps)))
	defer tick.Stop()

	var (
		games, withShots, uploaded, skipped, failed int64
		fatal                                       int32 // set on quota-exceeded → stop launching
		pingHashes                                  []string
		mu                                          sync.Mutex
	)
	start := time.Now()

	sem := make(chan struct{}, max(1, *concurrency))
	var wg sync.WaitGroup
	lastID := 0
	for atomic.LoadInt32(&fatal) == 0 {
		var rows []galgameRow
		q := db.DB().WithContext(ctx).
			Table("galgame").
			Select("id", "vndb_id").
			Where("id > ? AND status = 0 AND vndb_id LIKE 'v%'", lastID).
			Order("id ASC").
			Limit(*batch)
		if err := q.Scan(&rows).Error; err != nil {
			slog.Error("query batch", "err", err, "last_id", lastID)
			os.Exit(1)
		}
		if len(rows) == 0 {
			break
		}
		lastID = rows[len(rows)-1].ID

		for i := range rows {
			if atomic.LoadInt32(&fatal) != 0 {
				break
			}
			if *limit > 0 && atomic.LoadInt64(&games) >= int64(*limit) {
				break
			}
			scrs := vnMap[rows[i].VNDBID]
			atomic.AddInt64(&games, 1)
			if len(scrs) == 0 {
				continue // published game with no VNDB screenshots
			}
			atomic.AddInt64(&withShots, 1)

			wg.Add(1)
			sem <- struct{}{}
			go func(g galgameRow, scrs []string) {
				defer wg.Done()
				defer func() { <-sem }()
				res := processGame(ctx, g, scrs, meta, httpClient, cli, db, *dryRun, *vndbImageDir, tick)
				atomic.AddInt64(&uploaded, res.uploaded)
				atomic.AddInt64(&skipped, res.skipped)
				atomic.AddInt64(&failed, res.failed)
				if res.fatal {
					atomic.StoreInt32(&fatal, 1)
				}
				if len(res.hashes) > 0 {
					mu.Lock()
					pingHashes = append(pingHashes, res.hashes...)
					mu.Unlock()
				}
			}(rows[i], scrs)
		}
		wg.Wait() // per-page barrier (bounds memory; lastID already advanced)

		slog.Info("progress",
			"games", atomic.LoadInt64(&games),
			"games_with_shots", atomic.LoadInt64(&withShots),
			"uploaded", atomic.LoadInt64(&uploaded),
			"skipped", atomic.LoadInt64(&skipped),
			"failed", atomic.LoadInt64(&failed),
			"elapsed", time.Since(start).Truncate(time.Second),
		)
		if *limit > 0 && atomic.LoadInt64(&games) >= int64(*limit) {
			break
		}
	}
	wg.Wait()

	if atomic.LoadInt32(&fatal) != 0 {
		slog.Error("stopped early (image quota exceeded) — bump galgame_wiki image_quota_daily / image_quota_bytes_daily and re-run")
	}
	slog.Info("backfill finished",
		"games", games, "games_with_shots", withShots,
		"uploaded", uploaded, "skipped", skipped, "failed", failed,
		"elapsed", time.Since(start).Truncate(time.Second),
	)

	// Keep the freshly-uploaded hashes alive immediately (the daily
	// galgame-image-refping also covers galgame_screenshot, but don't wait a day).
	if !*dryRun && len(pingHashes) > 0 {
		slog.Info("final reference-ping for uploaded screenshots", "count", len(pingHashes))
		for _, b := range chunk(pingHashes, 1000) {
			if _, err := cli.ReferencePing(ctx, b); err != nil {
				slog.Warn("final ping batch failed", "err", err)
			}
		}
	}
}

type gameOutcome struct {
	uploaded, skipped, failed int64
	hashes                    []string
	fatal                     bool
}

// processGame uploads + inserts every not-yet-present VNDB screenshot of one game.
func processGame(
	ctx context.Context,
	g galgameRow,
	scrs []string,
	meta map[string]imgMeta,
	httpClient *http.Client,
	cli *imageclient.Client,
	db *database.PostgresDB,
	dryRun bool,
	vndbDir string,
	tick *time.Ticker,
) gameOutcome {
	var out gameOutcome

	// Resumability: skip scr already imported for this game.
	existing := map[string]struct{}{}
	if !dryRun {
		var keys []string
		if err := db.DB().WithContext(ctx).
			Table("galgame_screenshot").
			Where("galgame_id = ? AND source = 'vndb'", g.ID).
			Pluck("source_key", &keys).Error; err != nil {
			slog.Warn("load existing source_keys", "gid", g.ID, "err", err)
			out.failed++
			return out
		}
		for _, k := range keys {
			existing[k] = struct{}{}
		}
	}

	for idx, scr := range scrs {
		if _, ok := existing[scr]; ok {
			out.skipped++
			continue
		}
		body, err := fetchScreenshot(httpClient, vndbDir, scr)
		if err != nil {
			slog.Warn("fetch screenshot", "gid", g.ID, "scr", scr, "err", err)
			out.failed++
			continue
		}
		if dryRun {
			out.skipped++
			continue
		}

		<-tick.C // rate-limit the upload launch
		result, err := cli.Upload(ctx, bytes.NewReader(body), scr+".jpg", defaultPreset)
		if err != nil {
			if stderrors.Is(err, imageclient.ErrQuotaExceeded) {
				slog.Error("upload quota exceeded", "gid", g.ID, "scr", scr, "err", err)
				out.failed++
				out.fatal = true
				return out
			}
			slog.Warn("upload screenshot", "gid", g.ID, "scr", scr, "err", err)
			out.failed++
			continue
		}

		m := meta[scr] // zero value (0/0) if the scr is unknown in images — safe default
		if err := db.DB().WithContext(ctx).Exec(
			`INSERT INTO galgame_screenshot
			   (galgame_id, image_hash, sort_order, caption, sexual, violence, source, source_key, created)
			 VALUES (?, ?, ?, '', ?, ?, 'vndb', ?, NOW())
			 ON CONFLICT DO NOTHING`,
			g.ID, result.Hash, idx, m.sexual, m.violence, scr,
		).Error; err != nil {
			slog.Warn("insert galgame_screenshot", "gid", g.ID, "scr", scr, "err", err)
			out.failed++
			continue
		}
		out.uploaded++
		out.hashes = append(out.hashes, result.Hash)
	}
	return out
}

// fetchScreenshot returns the bytes for a VNDB screenshot id (e.g. "sf5707").
// Local rsync mirror first (sf/<id mod 100, 2-digit>/<id>.jpg, mirroring the
// t.vndb.org path), HTTP fallback only when the local file is missing/empty.
func fetchScreenshot(c *http.Client, vndbDir, scr string) ([]byte, error) {
	rel, err := scrRelPath(scr)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(vndbDir, filepath.FromSlash(rel))
	if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
		return b, nil
	}
	// Fallback: fetch from t.vndb.org (should not happen with a complete mirror).
	return httpGet(c, vndbImageHost+rel)
}

// scrRelPath maps a VNDB screenshot id ("sf5707") to its image path relative to
// the vndb-img root, mirroring the t.vndb.org layout: sf/<id mod 100>/<id>.jpg.
func scrRelPath(scr string) (string, error) {
	num := strings.TrimPrefix(scr, scrPrefix)
	n, err := strconv.Atoi(num)
	if err != nil {
		return "", fmt.Errorf("bad screenshot id %q", scr)
	}
	return fmt.Sprintf("sf/%02d/%s.jpg", n%100, num), nil
}

func httpGet(c *http.Client, url string) ([]byte, error) {
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 50<<20))
}

// loadScreenshotMap reads db/vn_screenshots (id<TAB>scr<TAB>rid) into
// vnID -> ordered, de-duplicated []scr. A scr can repeat across releases of the
// same VN; we keep first-occurrence order so sort_order is stable across runs.
func loadScreenshotMap(path string) (map[string][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string][]string{}
	seen := map[string]struct{}{} // "vn\tscr"
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		vn, scr := fields[0], fields[1]
		if vn == "" || scr == "" {
			continue
		}
		key := vn + "\t" + scr
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out[vn] = append(out[vn], scr)
	}
	return out, sc.Err()
}

// loadImageMeta reads db/images and keeps only sf* rows -> {sexual, violence}.
// Columns: id width height c_votecount c_sexual_avg c_sexual_stddev
//
//	c_violence_avg c_violence_stddev c_weight
func loadImageMeta(path string) (map[string]imgMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]imgMeta{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		if len(fields) < 9 {
			continue
		}
		id := fields[0]
		if !strings.HasPrefix(id, scrPrefix) {
			continue // only screenshots; skip ch*/cv*
		}
		sexAvg, _ := strconv.Atoi(fields[4])
		violAvg, _ := strconv.Atoi(fields[6])
		out[id] = imgMeta{sexual: rating(sexAvg), violence: rating(violAvg)}
	}
	return out, sc.Err()
}

func chunk[T any](src []T, n int) [][]T {
	var batches [][]T
	for i := 0; i < len(src); i += n {
		batches = append(batches, src[i:min(i+n, len(src))])
	}
	return batches
}
