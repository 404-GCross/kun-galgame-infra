// Package vndbscreenshots is the shared logic behind the sync-vndb-screenshots
// CLI and the scheduled "sync-vndb-screenshots" job: fill the gallery of every
// PUBLISHED galgame that has no VNDB screenshots yet, from the VNDB API, by
// uploading each shot into image_service (site=galgame_wiki, preset
// galgame_screenshot) and inserting galgame_screenshot rows (source="vndb",
// source_key=<sf-id>).
//
// API-only sibling of the cover sync (internal/jobs/vndbcovers): the one-time
// bulk was done offline (cmd/migrate-galgame-screenshots, 6-17); this job covers
// the ongoing trickle of newly-claimed games (VNDB caps screenshots at 10/VN,
// returned complete by /vn). Fill-only + idempotent: the default candidate is a
// published game with NO source="vndb" screenshot; within a game, existing
// source_keys are skipped and the INSERT is ON CONFLICT DO NOTHING, so re-runs /
// --ids top-ups never duplicate and a user's own screenshots are never touched.
// Writes galgame_screenshot rows only (no revision/snapshot) — same as the cover
// sync and the offline backfill. CLI and scheduler run identical code from here.
package vndbscreenshots

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	batchSize        = 100
	screenshotPreset = "galgame_screenshot"
	defaultTimeout   = 60 * time.Second
)

// Opts selects which games to fill. There is no dump mode (the bulk was an
// offline one-shot); this job is API-only.
type Opts struct {
	Apply        bool          // false = dry run (resolve + count, no upload/insert)
	Gap          time.Duration // min delay between VNDB API calls (default 2s)
	IDs          []int         // process exactly these ids (any status); enables top-up
	Limit        int
	Offset       int
	ImageBaseURL string // optional image_service base override
}

// Run fills VNDB screenshots for the selected games and returns a summary.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.Gap <= 0 {
		opts.Gap = 2 * time.Second
	}

	// Screenshots live under site=galgame_wiki, and upload + reference-ping are
	// site-scoped. In the oauth scheduler container ImageClient is the *account*
	// client, so prefer the dedicated galgame client; fall back to ImageClient
	// (CLI env-file / galgame container). Mirror galgame-image-refping.
	clientCfg := cfg.ImageClient
	if cfg.GalgameImageClient.ClientID != "" && cfg.GalgameImageClient.ClientSecret != "" {
		clientCfg = cfg.GalgameImageClient
	}
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("galgame image client not configured (set KUN_GALGAME_IMAGE_CLIENT_ID/SECRET on the oauth container, or KUN_IMAGE_CLIENT_ID/SECRET); refusing to run")
	}

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer db.Close()

	httpClient := &http.Client{Timeout: defaultTimeout}
	vc := vndb.New(opts.Gap)

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
	slog.Info("sync-vndb-screenshots start", "candidates", len(cands),
		"apply", opts.Apply, "client_id", clientCfg.ClientID)

	var uploaded, noShots, wouldUpload, failed int
	var pingHashes []string
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), uploaded, noShots, wouldUpload, failed), err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		shotsByVN, err := vc.FetchVNScreenshotsBatch(ctx, ids)
		if err != nil {
			failed += len(batch)
			slog.Error("fetch screenshots batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "err", err)
			continue
		}

		for _, c := range batch {
			shots := shotsByVN[c.VNDBID]
			if len(shots) == 0 {
				noShots++ // VNDB has no screenshots for this VN
				continue
			}
			if !opts.Apply {
				wouldUpload += len(shots)
				continue
			}
			hashes, fail, quota := processGame(ctx, db.DB(), cli, httpClient, c.ID, shots)
			uploaded += len(hashes)
			failed += fail
			pingHashes = append(pingHashes, hashes...)
			if quota {
				slog.Error("upload quota exceeded — bump galgame_wiki image_quota_daily and re-run")
				pingUploaded(ctx, cli, pingHashes)
				return summary(len(cands), uploaded, noShots, wouldUpload, failed),
					fmt.Errorf("image quota exceeded after %d screenshots", uploaded)
			}
		}
		slog.Info("progress", "processed", end, "of", len(cands),
			"uploaded", uploaded, "no_shots", noShots, "failed", failed)
	}

	// Keep freshly-uploaded screenshots alive immediately (galgame-image-refping
	// also covers galgame_screenshot daily, but don't wait for it).
	if opts.Apply {
		pingUploaded(ctx, cli, pingHashes)
	}

	slog.Info("sync-vndb-screenshots done", "candidates", len(cands), "uploaded", uploaded,
		"no_shots", noShots, "would_upload", wouldUpload, "failed", failed, "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), uploaded, noShots, wouldUpload, failed), nil
}

// processGame uploads + inserts the not-yet-present VNDB screenshots of one game.
// Returns the uploaded hashes, the per-shot failure count, and whether a quota
// error was hit (caller aborts the whole run).
func processGame(ctx context.Context, db *gorm.DB, cli *imageclient.Client, httpClient *http.Client, gid int, shots []vndb.VNScreenshot) (hashes []string, failed int, quota bool) {
	// Skip shots already imported for this game — keeps re-runs / --ids top-ups
	// from re-uploading. A fresh candidate has none.
	existing := map[string]struct{}{}
	var keys []string
	if err := db.WithContext(ctx).Table("galgame_screenshot").
		Where("galgame_id = ? AND source = 'vndb'", gid).
		Pluck("source_key", &keys).Error; err != nil {
		slog.Warn("load existing source_keys", "gid", gid, "err", err)
		return nil, 1, false
	}
	for _, k := range keys {
		existing[k] = struct{}{}
	}

	for idx, s := range shots {
		if _, ok := existing[s.ID]; ok {
			continue
		}
		body, err := httpGetLimited(httpClient, s.URL)
		if err != nil {
			failed++
			slog.Warn("download screenshot", "gid", gid, "scr", s.ID, "err", err)
			continue
		}
		res, err := cli.Upload(ctx, bytes.NewReader(body), s.ID+".jpg", screenshotPreset)
		if err != nil {
			if stderrors.Is(err, imageclient.ErrQuotaExceeded) {
				return hashes, failed, true
			}
			failed++
			slog.Warn("upload screenshot", "gid", gid, "scr", s.ID, "err", err)
			continue
		}
		// sort_order = position in VNDB's set; ON CONFLICT DO NOTHING dedups by
		// (galgame_id, image_hash) — no uniqueness on sort_order.
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO galgame_screenshot (galgame_id, image_hash, sort_order, caption, sexual, violence, source, source_key, created)
			 VALUES (?, ?, ?, '', ?, ?, 'vndb', ?, NOW())
			 ON CONFLICT DO NOTHING`,
			gid, res.Hash, idx, rating(s.Sexual), rating(s.Violence), s.ID,
		).Error; err != nil {
			failed++
			slog.Warn("insert galgame_screenshot", "gid", gid, "scr", s.ID, "err", err)
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

// httpGetLimited fetches a screenshot from t.vndb.org (≤50MB).
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

// rating maps a VNDB 0-2 average flag vote onto our int16 0-2 column
// (round-half-up, clamp).
func rating(avg float64) int16 {
	return int16(min(2, max(0, int(avg+0.5))))
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
	if len(opts.IDs) > 0 {
		// Targeted (any status); the NOT-EXISTS gate is dropped so a partially
		// filled game can be topped up — per-shot existing-key skip + ON CONFLICT
		// keep it dup-free.
		q = q.Where("id IN ?", opts.IDs)
	} else {
		q = q.Where("status = 0").
			Where("NOT EXISTS (SELECT 1 FROM galgame_screenshot s WHERE s.galgame_id = galgame.id AND s.source = 'vndb')").
			Offset(opts.Offset)
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

func summary(cands, uploaded, noShots, wouldUpload, failed int) map[string]any {
	return map[string]any{
		"candidates":   cands,
		"uploaded":     uploaded,
		"no_shots":     noShots,
		"would_upload": wouldUpload,
		"failed":       failed,
	}
}

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
