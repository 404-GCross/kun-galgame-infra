// Package portraitfill backfills a PORTRAIT cover for wiki galgames that have
// none. It is the U-02a "portrait fill" sibling of internal/jobs/vndbcovers:
// where that job fills the whole cover gallery, this one adds exactly one
// portrait cover to games whose existing covers are all landscape/near-square,
// so the upcoming portrait-first UI (kungal detail page + letmoe card) has a
// vertical image to show.
//
// Primary source is VNDB. For each candidate the VN main cover (vn.image) is
// fetched from the Kana API; its `dims` decide portrait-ness BEFORE any bytes
// are downloaded, so only usable images are pulled. A portrait main cover is
// downloaded from t.vndb.org, uploaded into image_service (site=galgame_wiki,
// preset galgame_banner) and recorded as a NON-pinned galgame_cover candidate
// (source="vndb", kind="main", source_key=<cv-id>). Pinning / sort_order / the
// portrait-pin schema are explicitly OUT of scope here (that is step 41) -- new
// rows are inserted at sort_order = max(existing)+1, never 0.
//
// Secondary/fallback source is Bangumi (api.bgm.tv): for the tiny set of
// candidates VNDB cannot give a portrait for AND that carry a bid, the subject
// large cover is tried (source="bangumi"). Bangumi's own nsfw flag is NOT used
// to infer a content rating (galgame_bangumi_meta doc), so those rows keep
// sexual/violence = 0.
//
// Idempotent: candidates are "no portrait cover yet" (so once a game has one it
// drops out), and per-game a source_key already present is skipped -- a second
// run writes nothing.
package portraitfill

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"image"
	"log/slog"
	"net/http"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	batchSize      = 100
	coverPreset    = "galgame_banner"
	kindMain       = "main"
	defaultTimeout = 60 * time.Second
)

// Opts configures a run.
type Opts struct {
	Apply        bool          // false = dry run: resolve candidates + fetch metadata + forecast only
	Limit        int           // max candidates to process (0 = all)
	Offset       int           // skip this many candidates (for chunking)
	APIGap       time.Duration // min delay between VNDB Kana calls (default 1s)
	DownloadGap  time.Duration // min delay between image downloads (default 350ms ~ 3 req/s)
	BGMGap       time.Duration // min delay between Bangumi API calls (default 700ms)
	BGMFallback  bool          // try Bangumi for candidates VNDB cannot cover (default true)
	ImageBaseURL string        // image_service base override (point at the LOCAL compose service, e.g. http://127.0.0.1:15006)
}

// candidate is a game needing a portrait cover: a published/VNDB-draft game
// with a VNDB anchor, at least one existing cover, and no portrait cover.
type candidate struct {
	ID     int
	VNDBID string
	BID    int
}

// runner carries the per-run dependencies and mutable counters (serial, so
// plain ints are fine).
type runner struct {
	db         *gorm.DB
	cli        *imageclient.Client
	httpClient *http.Client
	dlLimiter  *limiter
	pingHashes []string

	// apply counters
	downloaded, uploaded, rowsWritten, skippedDup, failed int
}

// Run resolves candidates, forecasts VNDB portrait availability, and (when
// Apply) fills portrait covers. It returns a summary suitable for logging.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.APIGap <= 0 {
		opts.APIGap = time.Second
	}
	if opts.DownloadGap <= 0 {
		opts.DownloadGap = 350 * time.Millisecond
	}
	if opts.BGMGap <= 0 {
		opts.BGMGap = 700 * time.Millisecond
	}

	// Covers live under site=galgame_wiki. Prefer the dedicated galgame client,
	// fall back to the generic image client (mirrors vndbcovers).
	clientCfg := cfg.ImageClient
	if cfg.GalgameImageClient.ClientID != "" && cfg.GalgameImageClient.ClientSecret != "" {
		clientCfg = cfg.GalgameImageClient
	}
	if opts.Apply && (clientCfg.ClientID == "" || clientCfg.ClientSecret == "") {
		return nil, fmt.Errorf("galgame image client not configured (set KUN_GALGAME_IMAGE_CLIENT_ID/SECRET or KUN_IMAGE_CLIENT_ID/SECRET); refusing to apply")
	}

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer wikiDB.Close()

	imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect images db (%s): %w", cfg.ImagesDatabase.DBName, err)
	}
	defer imagesDB.Close()

	dims, err := loadDims(ctx, imagesDB.DB())
	if err != nil {
		return nil, fmt.Errorf("load image dims: %w", err)
	}
	slog.Info("portrait-fill dims loaded", "images_with_dims", len(dims))

	cands, err := candidates(ctx, wikiDB.DB(), dims, opts)
	if err != nil {
		return nil, fmt.Errorf("resolve candidates: %w", err)
	}
	slog.Info("portrait-fill candidates", "candidates", len(cands), "apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)

	// ---- metadata forecast (VNDB main cover per candidate) ----
	meta, fetchFailedIDs, err := fetchVNDBImages(ctx, cands, opts.APIGap)
	if err != nil {
		return nil, fmt.Errorf("fetch vndb images: %w", err)
	}

	var portraitAvailable, landscapeOnly, noImage, fetchFailed int
	sizeDist := map[string]int{}
	// per-candidate classification, reused by the apply pass
	portrait := make(map[int]vndb.VNImage, len(cands)) // galgame_id -> portrait main cover
	for _, c := range cands {
		if fetchFailedIDs[c.VNDBID] {
			fetchFailed++
			continue
		}
		img, ok := meta[c.VNDBID]
		if !ok {
			noImage++
			continue
		}
		if len(img.Dims) >= 2 && isPortrait(int64(img.Dims[0]), int64(img.Dims[1])) {
			portraitAvailable++
			sizeDist[longEdgeBucket(img.Dims[1])]++
			portrait[c.ID] = img
		} else {
			landscapeOnly++
		}
	}
	metaFetched := portraitAvailable + landscapeOnly

	slog.Info("portrait-fill forecast",
		"candidates", len(cands), "meta_fetched", metaFetched,
		"portrait_available", portraitAvailable, "landscape_only", landscapeOnly,
		"no_image", noImage, "fetch_failed", fetchFailed)

	if !opts.Apply {
		slog.Info("DRY RUN -- nothing written; re-run with --apply")
		return summary(len(cands), metaFetched, portraitAvailable, landscapeOnly, noImage, fetchFailed, sizeDist, r0(), bgmZero()), nil
	}

	// ---- apply ----
	r := &runner{
		db:         wikiDB.DB(),
		httpClient: &http.Client{Timeout: defaultTimeout},
		dlLimiter:  newLimiter(opts.DownloadGap),
	}
	r.cli = imageclient.New(imageclient.Config{
		BaseURL:      resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL),
		CDNBase:      cfg.ImageService.CDNBase,
		ClientID:     clientCfg.ClientID,
		ClientSecret: clientCfg.ClientSecret,
		Timeout:      defaultTimeout,
	})
	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	defer hcancel()
	if err := r.cli.Health(hctx); err != nil {
		return nil, fmt.Errorf("image_service unreachable at %s: %w", resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL), err)
	}

	quota := false
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), metaFetched, portraitAvailable, landscapeOnly, noImage, fetchFailed, sizeDist, r.stats(), bgmZero()), err
		}
		img, ok := portrait[c.ID]
		if !ok {
			continue // no VNDB portrait -- handled by the BGM pass below
		}
		if q := r.fillCover(ctx, c.ID, "vndb", kindMain, img.ID, img.URL, dimsPixels(img.Dims), ratingFloat(img.Sexual), ratingFloat(img.Violence)); q {
			quota = true
			break
		}
	}

	bgm := bgmZero()
	if !quota && opts.BGMFallback {
		bgm = r.bgmPass(ctx, cands, portrait, meta, opts.BGMGap)
	}

	// Keep freshly-uploaded covers alive immediately.
	pingUploaded(ctx, r.cli, r.pingHashes)

	slog.Info("portrait-fill done",
		"rows_written", r.rowsWritten, "skipped_dup", r.skippedDup, "failed", r.failed,
		"bgm_rows_written", bgm["bgm_rows_written"])
	if quota {
		return summary(len(cands), metaFetched, portraitAvailable, landscapeOnly, noImage, fetchFailed, sizeDist, r.stats(), bgm),
			fmt.Errorf("image quota exceeded after %d covers", r.uploaded)
	}
	return summary(len(cands), metaFetched, portraitAvailable, landscapeOnly, noImage, fetchFailed, sizeDist, r.stats(), bgm), nil
}

// fillCover downloads one cover, uploads it, and inserts a NON-pinned
// galgame_cover row. Returns quota=true when the image quota is exhausted
// (caller aborts). All outcomes update the runner counters.
func (r *runner) fillCover(ctx context.Context, gid int, source, kind, sourceKey, url string, pixels int, sexual, violence int16) (quota bool) {
	maxSort, keys, err := loadExisting(ctx, r.db, gid)
	if err != nil {
		r.failed++
		slog.Warn("load existing covers", "gid", gid, "err", err)
		return false
	}
	if keys[sourceKey] {
		r.skippedDup++
		return false
	}
	body, err := httpGetLimited(r.httpClient, r.dlLimiter, url)
	if err != nil {
		r.failed++
		slog.Warn("download cover", "gid", gid, "src", source, "key", sourceKey, "err", err)
		return false
	}
	r.downloaded++
	body, skip, ferr := fitForUpload(body, pixels)
	if skip {
		r.failed++
		slog.Warn("skip oversized cover", "gid", gid, "key", sourceKey, "err", ferr)
		return false
	}
	res, err := r.cli.Upload(ctx, bytes.NewReader(body), sourceKey+".jpg", coverPreset)
	if err != nil {
		if stderrors.Is(err, imageclient.ErrQuotaExceeded) {
			return true
		}
		r.failed++
		slog.Warn("upload cover", "gid", gid, "key", sourceKey, "err", err)
		return false
	}
	r.uploaded++
	if err := r.db.WithContext(ctx).Exec(
		`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, kind, created)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW())
		 ON CONFLICT (galgame_id, image_hash) DO UPDATE
		   SET source = EXCLUDED.source, source_key = EXCLUDED.source_key, kind = EXCLUDED.kind`,
		gid, res.Hash, maxSort+1, sexual, violence, source, sourceKey, kind,
	).Error; err != nil {
		r.failed++
		slog.Warn("insert cover", "gid", gid, "key", sourceKey, "err", err)
		return false
	}
	r.rowsWritten++
	r.pingHashes = append(r.pingHashes, res.Hash)
	return false
}

// bgmPass is the fallback: for candidates without a VNDB portrait that carry a
// bid, try the Bangumi subject cover; fill only when it is actually portrait.
func (r *runner) bgmPass(ctx context.Context, cands []candidate, portrait map[int]vndb.VNImage, meta map[string]vndb.VNImage, gap time.Duration) map[string]any {
	bc := newBGMClient(gap)
	var considered, fetched, portraitCount, written, dup, failed int
	before := struct{ dl, up, rows, skip, fail int }{r.downloaded, r.uploaded, r.rowsWritten, r.skippedDup, r.failed}
	for _, c := range cands {
		if _, ok := portrait[c.ID]; ok {
			continue // already covered by VNDB
		}
		if c.BID <= 0 {
			continue
		}
		considered++
		bi, err := bc.FetchSubjectImage(ctx, c.BID)
		if err != nil {
			failed++
			slog.Warn("bgm fetch", "gid", c.ID, "bid", c.BID, "err", err)
			continue
		}
		if bi == nil {
			continue // no cover on Bangumi
		}
		fetched++
		// Bangumi omits dims -- download once, read the header for dims.
		body, err := httpGetLimited(r.httpClient, r.dlLimiter, bi.Large)
		if err != nil {
			failed++
			slog.Warn("bgm download", "gid", c.ID, "bid", c.BID, "err", err)
			continue
		}
		cfgImg, _, derr := image.DecodeConfig(bytes.NewReader(body))
		if derr != nil {
			failed++
			slog.Warn("bgm decode", "gid", c.ID, "bid", c.BID, "err", derr)
			continue
		}
		if !isPortrait(int64(cfgImg.Width), int64(cfgImg.Height)) {
			continue // Bangumi cover is not portrait -- skip
		}
		portraitCount++
		// Reuse the shared insert path via a direct upload (we already have bytes).
		outcome := r.insertPreloaded(ctx, c.ID, "bangumi", kindMain, fmt.Sprintf("bgm%d", bi.ID), body, cfgImg.Width*cfgImg.Height)
		switch outcome {
		case "written":
			written++
		case "dup":
			dup++
		default:
			failed++
		}
	}
	slog.Info("portrait-fill bgm pass", "considered", considered, "fetched", fetched,
		"portrait", portraitCount, "written", written, "dup", dup, "failed", failed,
		"net_new_downloads", r.downloaded-before.dl)
	return map[string]any{
		"bgm_considered":   considered,
		"bgm_fetched":      fetched,
		"bgm_portrait":     portraitCount,
		"bgm_rows_written": written,
		"bgm_skipped_dup":  dup,
		"bgm_failed":       failed,
	}
}

// insertPreloaded uploads bytes we already hold (Bangumi path) and inserts a
// non-pinned row. Returns "written" / "dup" / "failed".
func (r *runner) insertPreloaded(ctx context.Context, gid int, source, kind, sourceKey string, body []byte, pixels int) string {
	maxSort, keys, err := loadExisting(ctx, r.db, gid)
	if err != nil {
		return "failed"
	}
	if keys[sourceKey] {
		r.skippedDup++
		return "dup"
	}
	body, skip, _ := fitForUpload(body, pixels)
	if skip {
		r.failed++
		return "failed"
	}
	res, err := r.cli.Upload(ctx, bytes.NewReader(body), sourceKey+".jpg", coverPreset)
	if err != nil {
		r.failed++
		return "failed"
	}
	r.uploaded++
	if err := r.db.WithContext(ctx).Exec(
		`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, kind, created)
		 VALUES (?, ?, ?, 0, 0, ?, ?, ?, NOW())
		 ON CONFLICT (galgame_id, image_hash) DO UPDATE
		   SET source = EXCLUDED.source, source_key = EXCLUDED.source_key, kind = EXCLUDED.kind`,
		gid, res.Hash, maxSort+1, source, sourceKey, kind,
	).Error; err != nil {
		r.failed++
		return "failed"
	}
	r.rowsWritten++
	r.pingHashes = append(r.pingHashes, res.Hash)
	return "written"
}

func (r *runner) stats() map[string]int {
	return map[string]int{
		"downloaded":   r.downloaded,
		"uploaded":     r.uploaded,
		"rows_written": r.rowsWritten,
		"skipped_dup":  r.skippedDup,
		"failed":       r.failed,
	}
}

func r0() map[string]int {
	return map[string]int{"downloaded": 0, "uploaded": 0, "rows_written": 0, "skipped_dup": 0, "failed": 0}
}

func bgmZero() map[string]any {
	return map[string]any{"bgm_considered": 0, "bgm_fetched": 0, "bgm_portrait": 0, "bgm_rows_written": 0, "bgm_skipped_dup": 0, "bgm_failed": 0}
}

// loadExisting returns the max sort_order and the set of source_keys already on
// a game -- the dedup + non-pinned-insert inputs.
func loadExisting(ctx context.Context, db *gorm.DB, gid int) (maxSort int, keys map[string]bool, err error) {
	type ex struct {
		SortOrder int    `gorm:"column:sort_order"`
		SourceKey string `gorm:"column:source_key"`
	}
	var rows []ex
	if err = db.WithContext(ctx).Table("galgame_cover").
		Select("sort_order", "source_key").Where("galgame_id = ?", gid).Scan(&rows).Error; err != nil {
		return 0, nil, err
	}
	keys = make(map[string]bool, len(rows))
	for _, rr := range rows {
		if rr.SourceKey != "" {
			keys[rr.SourceKey] = true
		}
		if rr.SortOrder > maxSort {
			maxSort = rr.SortOrder
		}
	}
	return maxSort, keys, nil
}

func pingUploaded(ctx context.Context, cli *imageclient.Client, hashes []string) {
	for _, b := range chunk(hashes, 1000) {
		if _, err := cli.ReferencePing(ctx, b); err != nil {
			slog.Warn("reference-ping failed", "err", err)
		}
	}
}

func dimsPixels(d []int) int {
	if len(d) >= 2 {
		return d[0] * d[1]
	}
	return 0
}

func resolveBaseURL(cfg *config.Config, clientCfg config.ImageClientConfig, override string) string {
	if override != "" {
		return override
	}
	if clientCfg.BaseURL != "" {
		return clientCfg.BaseURL
	}
	return fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
}

func summary(cands, metaFetched, portraitAvailable, landscapeOnly, noImage, fetchFailed int, sizeDist map[string]int, apply map[string]int, bgm map[string]any) map[string]any {
	// Ordered long-edge buckets for stable logging.
	buckets := []string{"<512", "512-767", "768-1079", ">=1080"}
	dist := make(map[string]int, len(buckets))
	for _, b := range buckets {
		dist[b] = sizeDist[b]
	}
	out := map[string]any{
		"candidates":         cands,
		"meta_fetched":       metaFetched,
		"portrait_available": portraitAvailable,
		"landscape_only":     landscapeOnly,
		"no_image":           noImage,
		"fetch_failed":       fetchFailed,
		"portrait_size_dist": dist,
		"downloaded":         apply["downloaded"],
		"uploaded":           apply["uploaded"],
		"rows_written":       apply["rows_written"],
		"skipped_dup":        apply["skipped_dup"],
		"failed":             apply["failed"],
	}
	for k, v := range bgm {
		out[k] = v
	}
	return out
}
