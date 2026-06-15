// migrate-avatars backfills users.avatar_image_hash for users whose avatar is
// still a legacy CDN URL, by re-uploading that image through image_service
// (preset=avatar). Afterwards every avatar is image_service-managed (webp main
// + 256/100 variants, global dedup) — identical to a live upload via
// POST /auth/me/avatar.
//
// WHY one client: avatars are centralized — kungal/moyu both proxy avatar
// uploads to OAuth, which uploads under a single "account" OAuth client. This
// cmd reuses cfg.ImageClient (KUN_IMAGE_CLIENT_ID, = the account client in
// prod), so migrated avatars land in the exact same pool as live ones.
//
// FORMATS (verified on prod): image.kungal.com avatars are webp (pass straight
// through — image_service decodes webp), image.moyu.moe avatars are AVIF and the
// DB stores the small "-mini" variant. image_service's avatar preset only
// accepts jpeg/png/webp and its processor can't decode AVIF, so for moyu we
// (a) rewrite "-mini.<ext>" -> the full-size original, and (b) transcode AVIF
// (and any other non-webp/png/jpeg) -> PNG in-process before upload (plan A —
// the live image_service is untouched). image_service then re-encodes to webp.
//
//	go run ./cmd/migrate-avatars                 # DRY RUN: classify from DB + report. No network, no upload, no write.
//	go run ./cmd/migrate-avatars --probe 20      # download+transcode N samples, report. NO upload, NO write.
//	go run ./cmd/migrate-avatars --apply         # download -> transcode -> upload -> set avatar_image_hash
package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	// Decoders for image.Decode (side-effect registration). webp/png/jpeg are
	// passed through to image_service; gif and friends get transcoded to PNG.
	_ "image/gif"
	_ "image/jpeg"
	_ "time/tzdata"

	"github.com/gen2brain/avif" // pure-Go AVIF decoder (wazero); no cgo/system libs

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"
	"api/pkg/logger"
)

// candidate is a user with a legacy avatar URL and no image_service hash yet.
type candidate struct {
	ID     uint   `gorm:"column:id"`
	UUID   string `gorm:"column:uuid"`
	Avatar string `gorm:"column:avatar"`
}

func main() {
	apply := flag.Bool("apply", false, "perform the migration (default: dry run, nothing downloaded/uploaded/written)")
	probe := flag.Int("probe", 0, "download+transcode the first N candidates and report (validates the pipeline; NO upload, NO write)")
	limit := flag.Int("limit", 0, "cap candidates processed (0 = all)")
	concurrency := flag.Int("concurrency", 6, "parallel download+upload workers")
	manifestPath := flag.String("manifest", "", "write per-user CSV here (user_id,uuid,legacy_url,source_url,format,action). Use a writable path e.g. /tmp/avatars.csv")
	samples := flag.Int("samples", 12, "dry-run: how many sample source-URL rewrites to print")
	dlTimeout := flag.Duration("dl-timeout", 20*time.Second, "per-image download timeout")
	maxBytes := flag.Int64("max-bytes", 10<<20, "max bytes to download per image")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("connect oauth db", "error", err, "dbname", cfg.Database.DBName)
		os.Exit(1)
	}
	defer db.Close()
	gdb := db.DB()

	var rows []candidate
	if err := gdb.Raw(`
		SELECT id, uuid, avatar FROM users
		WHERE avatar <> '' AND (avatar_image_hash IS NULL OR avatar_image_hash = '')
		ORDER BY id
	`).Scan(&rows).Error; err != nil {
		slog.Error("query candidates", "error", err)
		os.Exit(1)
	}
	if *limit > 0 && *limit < len(rows) {
		rows = rows[:*limit]
	}

	// Classification report (always printed).
	byHost := map[string]int{}
	byExt := map[string]int{}
	for _, r := range rows {
		byHost[hostOf(r.Avatar)]++
		byExt[extOf(sourceURL(r.Avatar))]++
	}
	fmt.Printf("\n==================== CANDIDATES ====================\n")
	fmt.Printf("legacy-only users (avatar set, no hash): %d\n", len(rows))
	fmt.Printf("by host:\n")
	for h, n := range byHost {
		fmt.Printf("  · %-28s %d\n", h, n)
	}
	fmt.Printf("by source ext (after -mini rewrite):\n")
	for e, n := range byExt {
		fmt.Printf("  · %-8s %d\n", e, n)
	}

	// Pure dry run: no network, no upload, no write.
	if !*apply && *probe == 0 {
		fmt.Printf("\nsample source-URL rewrites (legacy -> source we'd fetch):\n")
		for i, r := range rows {
			if i >= *samples {
				break
			}
			src := sourceURL(r.Avatar)
			tag := ""
			if src != r.Avatar {
				tag = "   (rewritten)"
			}
			fmt.Printf("  #%d  %s\n      -> %s%s\n", r.ID, r.Avatar, src, tag)
		}
		fmt.Printf("\nDRY RUN — nothing fetched/uploaded/written. Use --probe N to test the pipeline, or --apply to run.\n")
		return
	}

	if cfg.ImageClient.ClientID == "" || cfg.ImageClient.ClientSecret == "" {
		slog.Error("image client not configured (set KUN_IMAGE_CLIENT_ID / KUN_IMAGE_CLIENT_SECRET / KUN_IMAGE_CLIENT_BASE_URL)")
		os.Exit(1)
	}
	imgCli := imageclient.New(imageclient.Config{
		BaseURL:      cfg.ImageClient.BaseURL,
		ClientID:     cfg.ImageClient.ClientID,
		ClientSecret: cfg.ImageClient.ClientSecret,
		Timeout:      90 * time.Second, // upload waits for processing (3 variants)
	})
	httpCli := &http.Client{Timeout: *dlTimeout}

	if *probe > 0 && *probe < len(rows) {
		rows = rows[:*probe]
	}
	mode := "APPLY"
	if !*apply {
		mode = fmt.Sprintf("PROBE %d (download+transcode only, NO upload/write)", len(rows))
	}
	fmt.Printf("\n%s — concurrency=%d\n", mode, *concurrency)

	var (
		mu                      sync.Mutex
		manifest                [][]string
		okCount, dedup, skipped int
	)
	record := func(c candidate, src, format, action string) {
		mu.Lock()
		manifest = append(manifest, []string{strconv.Itoa(int(c.ID)), c.UUID, c.Avatar, src, format, action})
		mu.Unlock()
	}

	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	for _, c := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func(c candidate) {
			defer wg.Done()
			defer func() { <-sem }()

			src := sourceURL(c.Avatar)
			raw, err := download(httpCli, src, *maxBytes)
			if err != nil {
				mu.Lock()
				skipped++
				mu.Unlock()
				record(c, src, "", "error:download:"+err.Error())
				return
			}
			up, format, err := normalize(raw)
			if err != nil {
				mu.Lock()
				skipped++
				mu.Unlock()
				record(c, src, format, "error:decode:"+err.Error())
				return
			}

			if !*apply { // probe: validated download+transcode, stop here
				mu.Lock()
				okCount++
				mu.Unlock()
				record(c, src, format, fmt.Sprintf("probe-ok:in=%d,out=%d", len(raw), len(up)))
				return
			}

			res, err := imgCli.Upload(context.Background(), bytes.NewReader(up), "avatar", "avatar")
			if err != nil {
				mu.Lock()
				skipped++
				mu.Unlock()
				record(c, src, format, "error:upload:"+err.Error())
				return
			}
			if err := gdb.Exec(`UPDATE users SET avatar_image_hash = ? WHERE id = ?`, res.Hash, c.ID).Error; err != nil {
				mu.Lock()
				skipped++
				mu.Unlock()
				record(c, src, format, "error:db:"+err.Error()+" hash="+res.Hash)
				return
			}
			mu.Lock()
			okCount++
			if res.Deduplicated {
				dedup++
			}
			mu.Unlock()
			action := "uploaded:" + res.Hash
			if res.Deduplicated {
				action = "dedup:" + res.Hash
			}
			record(c, src, format, action)
		}(c)
	}
	wg.Wait()

	if *manifestPath != "" {
		if err := writeCSV(*manifestPath, manifest); err != nil {
			slog.Error("write manifest", "error", err)
		} else {
			fmt.Printf("\nmanifest written: %s (%d rows)\n", *manifestPath, len(manifest))
		}
	}

	fmt.Printf("\n==================== SUMMARY ====================\n")
	fmt.Printf("processed: %d\n", len(rows))
	if *apply {
		fmt.Printf("  uploaded+set hash: %d (of which dedup hits: %d)\n", okCount, dedup)
	} else {
		fmt.Printf("  probe ok:          %d\n", okCount)
	}
	fmt.Printf("  skipped/errors:    %d  (see manifest for reasons)\n", skipped)
}

// sourceURL maps the stored avatar URL to the best image to actually fetch.
// moyu stores the small "-mini" thumbnail; the full-size original lives at the
// same path without "-mini", so prefer it for better quality.
func sourceURL(avatar string) string {
	if strings.Contains(avatar, "image.moyu.moe") {
		return strings.Replace(avatar, "-mini.", ".", 1)
	}
	return avatar
}

func hostOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func extOf(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndexByte(u, '.'); i >= 0 && i > strings.LastIndexByte(u, '/') {
		return strings.ToLower(u[i+1:])
	}
	return "(none)"
}

func download(cli *http.Client, url string, maxBytes int64) ([]byte, error) {
	resp, err := cli.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("exceeds max-bytes %d", maxBytes)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("empty body")
	}
	return b, nil
}

// avifDecodeMu serializes wazero-backed AVIF decodes across workers.
var avifDecodeMu sync.Mutex

// normalize returns bytes ready for image_service's avatar preset. webp/png/jpeg
// pass through (image_service decodes them); everything else (avif, gif, …) is
// decoded and re-encoded to PNG. Returns the detected/source format label.
func normalize(raw []byte) ([]byte, string, error) {
	switch f := sniff(raw); f {
	case "webp", "png", "jpeg":
		return raw, f, nil
	case "avif":
		// avif.Decode runs a wasm module via wazero; serialize to be safe under
		// the concurrent worker pool (decode is ~ms, so this isn't a bottleneck).
		avifDecodeMu.Lock()
		img, err := avif.Decode(bytes.NewReader(raw))
		avifDecodeMu.Unlock()
		if err != nil {
			return nil, f, err
		}
		return encodePNG(img, f)
	default:
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, f, fmt.Errorf("unsupported/undecodable (%s): %w", f, err)
		}
		return encodePNG(img, f)
	}
}

func encodePNG(img image.Image, srcFormat string) ([]byte, string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, srcFormat, err
	}
	return buf.Bytes(), srcFormat + "->png", nil
}

// sniff identifies the image format by magic bytes (the legacy CDNs all return
// application/octet-stream, so content-type is useless). AVIF is an ISO-BMFF
// box: [size][ftyp][major brand …] with "avif"/"avis" among the brands.
func sniff(b []byte) string {
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

func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"user_id", "uuid", "legacy_url", "source_url", "format", "action"}); err != nil {
		return err
	}
	return w.WriteAll(rows)
}
