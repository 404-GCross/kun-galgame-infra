// Package vndbcovers is the shared logic behind the sync-vndb-covers CLI and the
// scheduled "sync-vndb-covers" job: fill the cover gallery of PUBLISHED galgames
// from VNDB — the VN main cover (vn.image) PLUS every release cover (the /cv page:
// pkgfront/dig/pkgback/pkgcontent/pkgside/pkgmed) — uploading each into
// image_service (site=galgame_wiki) and recording it in galgame_cover with a
// `kind` label (source="vndb", source_key=<cv-id>). One is pinned (sort_order=0,
// the effective banner); the rest are non-pinned candidates a "view all covers"
// gallery can group by kind.
//
// Two byte sources share one upload/upsert path:
//   - DUMP mode (Opts.VNDBImageDir set) — bulk backfill off the offline VNDB dump
//     (db/vn main + db/releases_vn + db/releases_images + db/images ratings + cv/
//     tree). No VNDB API hammering — the established bulk technique.
//   - API mode (default; the daily job) — /vn image{} + paginated /release images{}
//     for the trickle of newly-claimed games; bytes from t.vndb.org.
//
// Default candidate = published game (status=0, vndb_id) with NO cover yet (new /
// claimed games get the full set on first sync). Opts.All drops that gate for the
// one-time backfill over every published vndb game (idempotent: existing cv-ids
// are skipped, and the INSERT upserts by (galgame_id,image_hash) so a cover shared
// with a migrated row is relabeled, never duplicated). Writes galgame_cover rows
// only (no revision/snapshot) — the daily galgame-image-refping pins them; an edit
// preserves them via the source="vndb" union in overlayUpdate.
package vndbcovers

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder for image.Decode
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/jobs/galgametouch"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/imageclient"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder for image.Decode

	"gorm.io/gorm"
)

const (
	batchSize      = 100
	coverPreset    = "galgame_banner"
	defaultTimeout = 60 * time.Second
	vndbImageHost  = "https://t.vndb.org/"
	kindMain       = "main" // the VN's primary cover (vn.image); release covers use their VNDB type

	// Upload-fit limits (fitForUpload). The image service rejects bodies over its
	// Fiber BodyLimit (4 MiB) and images over MaxDecodedPixels (50 MP); we
	// downscale to stay safely under BOTH before uploading.
	maxUploadBytes   = 3_500_000   // under the 4 MiB BodyLimit, with margin for multipart overhead
	maxPixels        = 50_000_000  // == processor MaxDecodedPixels (decode-bomb guard)
	safeDecodePixels = 200_000_000 // refuse to decode beyond this (OOM guard); dump max is ~112 MP
	downscaleLongest = 2560        // cap the longest side when downscaling
)

// Opts selects the games to fill and where the cover bytes come from.
type Opts struct {
	Apply       bool          // false = dry run (resolve + count, no upload/upsert)
	Gap         time.Duration // min delay between VNDB API calls (API mode; default 2s)
	All         bool          // drop the "no cover yet" gate — process every published vndb game (backfill)
	Concurrency int           // parallel per-game workers (default 1). Safe to raise in DUMP mode (local reads); keep 1 in API mode to not hammer t.vndb.org
	IDs         []int         // process exactly these ids (any status); else published-only
	Limit       int
	Offset      int

	// Dump mode (one-time bulk). When VNDBImageDir is set, covers + ratings come
	// from the offline dump instead of the VNDB API.
	VNDBImageDir      string // rsync mirror of rsync://dl.vndb.org/vndb-img (must contain cv/)
	VNDumpPath        string // dump's db/vn            (v-id -> main cover cv-id)
	ImagesDumpPath    string // dump's db/images        (cv-id -> sexual/violence)
	RelVNDumpPath     string // dump's db/releases_vn   (release -> vn)
	RelImagesDumpPath string // dump's db/releases_images (release -> cover cv-id + type)

	ImageBaseURL string // optional image_service base override (else the client's configured base)
}

// coverItem is one cover in a VN's set; bytes are fetched separately (and only
// when applying) via fetchCover, since a VNDB cover URL is deterministic from cv-id.
type coverItem struct {
	cvID             string
	kind             string
	sexual, violence int16
	pixels           int // width*height (0 = unknown); used to pre-check the pixel limit
}

// coverSource yields the full, deduped cover set per VN (main first). The byte
// fetch is shared (fetchCover) — only the metadata source differs (dump vs API).
type coverSource interface {
	prefetch(ctx context.Context, vndbIDs []string) error
	lookup(vndbID string) []coverItem
}

// apiSource resolves cover metadata from the VNDB API (daily trickle): /vn for the
// main cover + /release for release covers.
type apiSource struct {
	vc    *vndb.Client
	cache map[string][]coverItem
}

func newAPISource(gap time.Duration) *apiSource {
	return &apiSource{vc: vndb.New(gap), cache: map[string][]coverItem{}}
}

func (a *apiSource) prefetch(ctx context.Context, ids []string) error {
	mains, err := a.vc.FetchVNImagesBatch(ctx, ids)
	if err != nil {
		return fmt.Errorf("fetch main covers: %w", err)
	}
	rels, err := a.vc.FetchVNReleaseCoversBatch(ctx, ids)
	if err != nil {
		return fmt.Errorf("fetch release covers: %w", err)
	}
	for _, id := range ids {
		seen := map[string]bool{}
		var items []coverItem
		if m, ok := mains[id]; ok {
			items = append(items, coverItem{cvID: m.ID, kind: kindMain, sexual: ratingFloat(m.Sexual), violence: ratingFloat(m.Violence), pixels: dimsToPixels(m.Dims)})
			seen[m.ID] = true
		}
		for _, rc := range rels[id] {
			if seen[rc.ID] {
				continue
			}
			seen[rc.ID] = true
			items = append(items, coverItem{cvID: rc.ID, kind: rc.Type, sexual: ratingFloat(rc.Sexual), violence: ratingFloat(rc.Violence), pixels: dimsToPixels(rc.Dims)})
		}
		if len(items) > 0 {
			a.cache[id] = items
		}
	}
	return nil
}

func (a *apiSource) lookup(vndbID string) []coverItem { return a.cache[vndbID] }

// ratingFloat maps a VNDB API 0-2 average flag vote onto our int16 0-2 column
// (reuses the dump rating's round-half-up + clamp by scaling to the 0-200 form).
func ratingFloat(avg float64) int16 { return rating(int(avg * 100)) }

// Run fills cover galleries for the selected games and returns a summary.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.Gap <= 0 {
		opts.Gap = 2 * time.Second
	}

	// Covers live under site=galgame_wiki; upload + reference-ping are site-scoped.
	// In the oauth scheduler container ImageClient is the *account* client, so
	// prefer the dedicated galgame client; fall back to ImageClient (CLI env-file /
	// galgame container). Mirror galgame-image-refping.
	clientCfg := cfg.ImageClient
	if cfg.GalgameImageClient.ClientID != "" && cfg.GalgameImageClient.ClientSecret != "" {
		clientCfg = cfg.GalgameImageClient
	}
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("galgame image client not configured (set KUN_GALGAME_IMAGE_CLIENT_ID/SECRET on the oauth container, or KUN_IMAGE_CLIENT_ID/SECRET); refusing to run")
	}

	var src coverSource
	mode := "api"
	if opts.VNDBImageDir != "" {
		mode = "dump"
		ds, err := newDumpSource(opts.VNDumpPath, opts.ImagesDumpPath, opts.RelVNDumpPath, opts.RelImagesDumpPath)
		if err != nil {
			return nil, fmt.Errorf("init dump source: %w", err)
		}
		src = ds
	} else {
		src = newAPISource(opts.Gap)
	}

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer db.Close()

	// A landed cover changes what the claimed work shows, so the changes feed
	// has to learn about it. Opened only when applying — a dry run keeps a nil
	// Toucher, which is a no-op.
	var toucher *galgametouch.Toucher
	if opts.Apply {
		toucher, err = galgametouch.Open(cfg)
		if err != nil {
			return nil, err
		}
		defer toucher.Close()
	}

	httpClient := &http.Client{Timeout: defaultTimeout}

	var cli *imageclient.Client
	if opts.Apply {
		cli = imageclient.New(imageclient.Config{
			BaseURL:      resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL),
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     clientCfg.ClientID,
			ClientSecret: clientCfg.ClientSecret,
			Timeout:      defaultTimeout,
		})
		hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
		defer hcancel()
		if err := cli.Health(hctx); err != nil {
			return nil, fmt.Errorf("image_service unreachable: %w", err)
		}
	}

	cands, err := candidates(db.DB(), opts)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	slog.Info("sync-vndb-covers start", "candidates", len(cands), "mode", mode,
		"all", opts.All, "apply", opts.Apply, "client_id", clientCfg.ClientID)

	conc := max(1, opts.Concurrency)
	var (
		uploaded, noCover, wouldUpload, failed int64
		quotaHit                               int32
		pingHashes                             []string
		// batchTouched collects the galgames of THIS batch that really gained a
		// cover; it is flushed and reset at the batch barrier below.
		batchTouched []int
		mu           sync.Mutex
		sem          = make(chan struct{}, conc)
		wg           sync.WaitGroup
	)
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), int(uploaded), int(noCover), int(wouldUpload), int(failed), toucher.Count()), err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		if err := src.prefetch(ctx, ids); err != nil {
			// VNDB down / out-of-range id: skip the batch, retry next run.
			atomic.AddInt64(&failed, int64(len(batch)))
			slog.Error("prefetch batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "err", err)
			continue
		}

		// Per-game workers (parallel up to conc). Different games touch different
		// galgame_ids, so the pinned/sort_order logic stays per-game-serial inside
		// processGame; only the slow upload+upsert fans out.
		for _, c := range batch {
			if atomic.LoadInt32(&quotaHit) != 0 {
				break
			}
			items := src.lookup(c.VNDBID)
			if len(items) == 0 {
				atomic.AddInt64(&noCover, 1) // VNDB has no covers for this VN
				continue
			}
			if !opts.Apply {
				atomic.AddInt64(&wouldUpload, int64(len(items)))
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(c candidate, items []coverItem) {
				defer wg.Done()
				defer func() { <-sem }()
				hashes, fail, quota := processGame(ctx, db.DB(), cli, httpClient, opts.VNDBImageDir, c.ID, items)
				atomic.AddInt64(&uploaded, int64(len(hashes)))
				atomic.AddInt64(&failed, int64(fail))
				if len(hashes) > 0 {
					mu.Lock()
					pingHashes = append(pingHashes, hashes...)
					// At least one cover row landed for this game — a game whose
					// covers were all already present, or all failed, adds nothing.
					batchTouched = append(batchTouched, c.ID)
					mu.Unlock()
				}
				if quota {
					atomic.StoreInt32(&quotaHit, 1)
				}
			}(c, items)
		}
		wg.Wait() // barrier per batch (bounds memory + lets progress log)
		// Stamp before the quota check so an aborted run still carries the
		// watermarks its committed covers earned.
		if err := toucher.Touch(ctx, batchTouched); err != nil {
			slog.Error("touch claimed works", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "err", err)
		}
		batchTouched = batchTouched[:0]
		slog.Info("progress", "processed", end, "of", len(cands),
			"uploaded", atomic.LoadInt64(&uploaded), "no_cover", atomic.LoadInt64(&noCover),
			"failed", atomic.LoadInt64(&failed), "touched", toucher.Count())
		if atomic.LoadInt32(&quotaHit) != 0 {
			slog.Error("upload quota exceeded — bump galgame_wiki image_quota_daily and re-run")
			pingUploaded(ctx, cli, pingHashes)
			return summary(len(cands), int(uploaded), int(noCover), int(wouldUpload), int(failed), toucher.Count()),
				fmt.Errorf("image quota exceeded after %d covers", uploaded)
		}
	}

	// Keep freshly-uploaded covers alive immediately (galgame-image-refping also
	// covers galgame_cover daily, but don't wait for it).
	if opts.Apply {
		pingUploaded(ctx, cli, pingHashes)
	}

	slog.Info("sync-vndb-covers done", "candidates", len(cands), "uploaded", uploaded,
		"no_cover", noCover, "would_upload", wouldUpload, "failed", failed,
		"touched", toucher.Count(), "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), int(uploaded), int(noCover), int(wouldUpload), int(failed), toucher.Count()), nil
}

// processGame uploads + upserts the not-yet-present covers of one game. Returns the
// processed hashes, the per-cover failure count, and whether a quota error was hit
// (caller aborts the run). The pinned cover (sort_order=0) is left as-is when the
// game already has one; otherwise the first cover (main-first ordering) is pinned.
func processGame(ctx context.Context, db *gorm.DB, cli *imageclient.Client, httpClient *http.Client, vndbDir string, gid int, items []coverItem) (hashes []string, failed int, quota bool) {
	type ex struct {
		SortOrder int    `gorm:"column:sort_order"`
		SourceKey string `gorm:"column:source_key"`
	}
	var rows []ex
	if err := db.WithContext(ctx).Table("galgame_cover").
		Select("sort_order", "source_key").Where("galgame_id = ?", gid).Scan(&rows).Error; err != nil {
		slog.Warn("load existing covers", "gid", gid, "err", err)
		return nil, 1, false
	}
	existingKeys := make(map[string]bool, len(rows))
	hasPinned := false
	maxSort := 0
	for _, r := range rows {
		if r.SourceKey != "" {
			existingKeys[r.SourceKey] = true
		}
		if r.SortOrder == 0 {
			hasPinned = true
		}
		if r.SortOrder > maxSort {
			maxSort = r.SortOrder
		}
	}

	for _, it := range items {
		if existingKeys[it.cvID] {
			continue // already synced — skip the re-upload
		}
		body, err := fetchCover(httpClient, vndbDir, it.cvID)
		if err != nil {
			failed++
			slog.Warn("fetch cover", "gid", gid, "cv", it.cvID, "err", err)
			continue
		}
		// Downscale to fit the image service's byte + pixel limits (skip the rare
		// un-decodable / absurdly-large image rather than fail the whole game).
		body, skip, ferr := fitForUpload(body, it.pixels)
		if skip {
			failed++
			slog.Warn("skip oversized cover", "gid", gid, "cv", it.cvID, "pixels", it.pixels, "err", ferr)
			continue
		}
		res, err := cli.Upload(ctx, bytes.NewReader(body), it.cvID+".jpg", coverPreset)
		if err != nil {
			if stderrors.Is(err, imageclient.ErrQuotaExceeded) {
				return hashes, failed, true
			}
			failed++
			slog.Warn("upload cover", "gid", gid, "cv", it.cvID, "err", err)
			continue
		}
		so := 0
		if hasPinned {
			maxSort++
			so = maxSort
		} else {
			hasPinned = true // no pinned yet → pin this one (items are main-first)
		}
		// Upsert: insert new; on (galgame_id,image_hash) conflict — e.g. a migrated
		// row sharing this image — (re)label it as the vndb cover WITHOUT disturbing
		// its existing pin/order. sort_order is only ever set to 0 when no pin
		// existed, so the partial-unique pinned index never conflicts here.
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, kind, created)
			 VALUES (?, ?, ?, ?, ?, 'vndb', ?, ?, NOW())
			 ON CONFLICT (galgame_id, image_hash) DO UPDATE
			   SET source = 'vndb', source_key = EXCLUDED.source_key, kind = EXCLUDED.kind`,
			gid, res.Hash, so, it.sexual, it.violence, it.cvID, it.kind,
		).Error; err != nil {
			failed++
			slog.Warn("upsert cover", "gid", gid, "cv", it.cvID, "err", err)
			continue
		}
		hashes = append(hashes, res.Hash)
	}
	return hashes, failed, false
}

func pingUploaded(ctx context.Context, cli *imageclient.Client, hashes []string) {
	for _, b := range chunk(hashes, 1000) {
		if _, err := cli.ReferencePing(ctx, b); err != nil {
			slog.Warn("reference-ping failed", "err", err)
		}
	}
}

// fetchCover returns the bytes for a VNDB cover id ("cv12345"). When vndbDir is
// set (dump mode) the local rsync mirror is read first; otherwise (and as a
// fallback) the deterministic t.vndb.org URL is fetched.
func fetchCover(c *http.Client, vndbDir, cvID string) ([]byte, error) {
	rel, err := cvRelPath(cvID)
	if err != nil {
		return nil, err
	}
	if vndbDir != "" {
		if b, err := readLocal(vndbDir, rel); err == nil && len(b) > 0 {
			return b, nil
		}
	}
	return httpGetLimited(c, vndbImageHost+rel)
}

// dimsToPixels turns VNDB's [w,h] dims array into a pixel count (0 if unknown).
func dimsToPixels(d []int) int {
	if len(d) >= 2 {
		return d[0] * d[1]
	}
	return 0
}

// fitForUpload returns bytes safe to upload — within both the byte (BodyLimit) and
// pixel (processor) limits. Within-limit images pass through untouched (no decode).
// Oversized ones are decoded + downscaled (longest side ≤ downscaleLongest) and
// re-encoded as JPEG. Images that can't be decoded, or are so large that decoding
// risks OOM, return skip=true (caller logs + skips — no URL fallback).
func fitForUpload(body []byte, pixels int) (out []byte, skip bool, err error) {
	if len(body) <= maxUploadBytes && (pixels == 0 || pixels <= maxPixels) {
		return body, false, nil
	}
	if pixels > safeDecodePixels {
		return nil, true, fmt.Errorf("%d MP exceeds safe-decode limit", pixels/1_000_000)
	}
	src, _, derr := image.Decode(bytes.NewReader(body))
	if derr != nil {
		return nil, true, fmt.Errorf("decode: %w", derr)
	}
	resized := downscale(src, downscaleLongest)
	for _, q := range []int{82, 68, 55} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: q}); err != nil {
			return nil, true, fmt.Errorf("encode: %w", err)
		}
		if buf.Len() <= maxUploadBytes {
			return buf.Bytes(), false, nil
		}
	}
	// Still too big after quality drops — shrink once more.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, downscale(resized, 1600), &jpeg.Options{Quality: 70}); err != nil {
		return nil, true, fmt.Errorf("encode: %w", err)
	}
	if buf.Len() > maxUploadBytes {
		return nil, true, fmt.Errorf("still %d bytes after downscale", buf.Len())
	}
	return buf.Bytes(), false, nil
}

// downscale returns src resized so its longest side is ≤ longest (aspect kept),
// or src unchanged if already within bounds. Uses high-quality CatmullRom.
func downscale(src image.Image, longest int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= longest && h <= longest {
		return src
	}
	nw, nh := longest, longest
	if w >= h {
		nh = h * longest / w
	} else {
		nw = w * longest / h
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}

func httpGetLimited(c *http.Client, url string) ([]byte, error) {
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

type candidate struct {
	ID     int
	VNDBID string `gorm:"column:vndb_id"`
}

func candidates(db *gorm.DB, opts Opts) ([]candidate, error) {
	q := db.Model(&model.Galgame{}).
		Select("id", "vndb_id").
		Where("vndb_id ~ '^v[0-9]+$'").
		Order("id")
	switch {
	case len(opts.IDs) > 0:
		q = q.Where("id IN ?", opts.IDs) // targeted (any status)
	default:
		q = q.Where("status = 0")
		if !opts.All {
			// Default: only cover-less games (new/claimed). --all drops this for the
			// one-time backfill that adds release covers to existing games.
			q = q.Where("NOT EXISTS (SELECT 1 FROM galgame_cover c WHERE c.galgame_id = galgame.id)")
		}
		q = q.Offset(opts.Offset)
		if opts.Limit > 0 {
			q = q.Limit(opts.Limit)
		}
	}
	var cands []candidate
	return cands, q.Scan(&cands).Error
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

func summary(cands, uploaded, noCover, wouldUpload, failed, touched int) map[string]any {
	return map[string]any{
		"candidates":   cands,
		"uploaded":     uploaded,
		"no_cover":     noCover,
		"would_upload": wouldUpload,
		"failed":       failed,
		"touched":      touched,
	}
}

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
