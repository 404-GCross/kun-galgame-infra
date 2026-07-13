package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	coverPreset    = "galgame_banner"
	stemSep        = "__" // export filename = <gid>__<origHash>.<ext>
	defaultTimeout = 60 * time.Second
)

// runExport downloads the <1080 best portraits (that have not already been
// upscaled) from the production CDN — read-only — into dir, named
// <gid>__<origHash>.webp so upscale-bench's output (same stem, .webp) round-trips
// back to (gid, origHash) at reinject time.
func runExport(ctx context.Context, cfg *config.Config, sels []selection, dir string, limit int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cdnBase := cfg.ImageService.CDNBase
	httpClient := &http.Client{Timeout: defaultTimeout}
	var exported, failed int
	for _, s := range sels {
		if s.State != stateNeedUpscale || s.HasUpscale {
			continue // only un-upscaled <1080 best portraits
		}
		if limit > 0 && exported >= limit {
			break
		}
		url := imageclient.MainURL(cdnBase, s.Best.Hash, "webp")
		body, err := httpGet(httpClient, url)
		if err != nil {
			failed++
			slog.Warn("export download", "gid", s.GameID, "hash", s.Best.Hash, "err", err)
			continue
		}
		out := filepath.Join(dir, fmt.Sprintf("%d%s%s.webp", s.GameID, stemSep, s.Best.Hash))
		if err := os.WriteFile(out, body, 0o644); err != nil {
			failed++
			slog.Warn("export write", "file", out, "err", err)
			continue
		}
		exported++
	}
	slog.Info("export done", "exported", exported, "dir", dir, "failed", failed)
	return nil
}

// runReinject uploads each upscaled webp to the LOCAL image service, inserts a
// NEW source='upscale' cover row (source_key = original hash, kind inherited),
// and pins it. Idempotent: a game that already carries the (source='upscale',
// source_key=origHash) row is skipped, so a second run writes nothing.
func runReinject(ctx context.Context, cfg *config.Config, db *gorm.DB, dir, imageBaseURL string, apply bool) error {
	files, err := listWebp(dir)
	if err != nil {
		return err
	}
	slog.Info("reinject scan", "dir", dir, "files", len(files), "apply", apply)
	if !apply {
		slog.Info("DRY RUN — re-run with --apply --image-base-url http://127.0.0.1:15006 to write")
		return nil
	}
	if imageBaseURL == "" {
		return errors.New("--image-base-url required for reinject --apply (point at the LOCAL compose service, e.g. http://127.0.0.1:15006)")
	}

	clientCfg := cfg.ImageClient
	if cfg.GalgameImageClient.ClientID != "" && cfg.GalgameImageClient.ClientSecret != "" {
		clientCfg = cfg.GalgameImageClient
	}
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return errors.New("galgame image client not configured (KUN_GALGAME_IMAGE_CLIENT_ID/SECRET or KUN_IMAGE_CLIENT_ID/SECRET)")
	}
	cli := imageclient.New(imageclient.Config{
		BaseURL:      imageBaseURL,
		CDNBase:      cfg.ImageService.CDNBase,
		ClientID:     clientCfg.ClientID,
		ClientSecret: clientCfg.ClientSecret,
		Timeout:      defaultTimeout,
	})
	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	defer hcancel()
	if err := cli.Health(hctx); err != nil {
		return fmt.Errorf("image_service unreachable at %s: %w", imageBaseURL, err)
	}

	var written, skipDup, pinned, failed int
	var pinged []string
	for _, f := range files {
		gid, origHash, ok := parseStem(f)
		if !ok {
			failed++
			slog.Warn("reinject: unparseable filename", "file", f)
			continue
		}
		// Idempotent dedup: this upscale already reinjected?
		var cnt int64
		if err := db.WithContext(ctx).Table("galgame_cover").
			Where("galgame_id = ? AND source = 'upscale' AND source_key = ?", gid, origHash).
			Count(&cnt).Error; err != nil {
			failed++
			slog.Warn("reinject dedup query", "gid", gid, "err", err)
			continue
		}
		if cnt > 0 {
			skipDup++
			continue
		}
		orig, maxSort, err := loadOrigCover(ctx, db, gid, origHash)
		if err != nil {
			failed++
			slog.Warn("reinject load orig", "gid", gid, "hash", origHash, "err", err)
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			failed++
			slog.Warn("reinject read", "file", f, "err", err)
			continue
		}
		res, err := cli.Upload(ctx, bytes.NewReader(body), origHash+".webp", coverPreset)
		if err != nil {
			failed++
			slog.Warn("reinject upload", "gid", gid, "err", err)
			continue
		}
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, kind, portrait_pinned, created)
			 VALUES (?, ?, ?, ?, ?, 'upscale', ?, ?, false, NOW())
			 ON CONFLICT (galgame_id, image_hash) DO UPDATE
			   SET source = EXCLUDED.source, source_key = EXCLUDED.source_key, kind = EXCLUDED.kind`,
			gid, res.Hash, maxSort+1, orig.Sexual, orig.Violence, origHash, orig.Kind,
		).Error; err != nil {
			failed++
			slog.Warn("reinject insert", "gid", gid, "err", err)
			continue
		}
		written++
		pinged = append(pinged, res.Hash)
		if err := pinPortrait(ctx, db, gid, res.Hash); err != nil {
			failed++
			slog.Warn("reinject pin", "gid", gid, "err", err)
			continue
		}
		pinned++
	}
	if len(pinged) > 0 {
		if _, err := cli.ReferencePing(ctx, pinged); err != nil {
			slog.Warn("reference-ping", "err", err)
		}
	}
	slog.Info("reinject done", "rows_written", written, "skipped_dup", skipDup, "pinned", pinned, "failed", failed)
	return nil
}

// origCover carries the fields a reinjected upscale row inherits from its source.
type origCover struct {
	Kind     string `gorm:"column:kind"`
	Sexual   int16  `gorm:"column:sexual"`
	Violence int16  `gorm:"column:violence"`
}

// loadOrigCover returns the source cover's inherited fields + the game's current
// max sort_order (the new upscale row lands at max+1, never the 0 pin slot).
func loadOrigCover(ctx context.Context, db *gorm.DB, gid int, origHash string) (origCover, int, error) {
	var oc origCover
	if err := db.WithContext(ctx).Table("galgame_cover").
		Select("kind, sexual, violence").
		Where("galgame_id = ? AND image_hash = ?", gid, origHash).
		Take(&oc).Error; err != nil {
		return oc, 0, err
	}
	var maxSort *int
	if err := db.WithContext(ctx).Table("galgame_cover").
		Select("MAX(sort_order)").Where("galgame_id = ?", gid).Scan(&maxSort).Error; err != nil {
		return oc, 0, err
	}
	m := 0
	if maxSort != nil {
		m = *maxSort
	}
	return oc, m, nil
}

// listWebp returns every .webp file under dir (recursive, sorted by Walk order).
func listWebp(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".webp") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// parseStem recovers (gid, origHash) from an export/output filename
// <gid>__<origHash>.webp.
func parseStem(path string) (gid int, origHash string, ok bool) {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	i := strings.Index(stem, stemSep)
	if i <= 0 {
		return 0, "", false
	}
	g, err := strconv.Atoi(stem[:i])
	if err != nil {
		return 0, "", false
	}
	h := stem[i+len(stemSep):]
	if len(h) != 64 {
		return 0, "", false
	}
	return g, h, true
}

// httpGet fetches url, capping the body at 50 MiB.
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
