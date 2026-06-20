// Package vndbcovers is the shared logic behind the sync-vndb-covers CLI and the
// scheduled "sync-vndb-covers" job: fill the cover of every PUBLISHED galgame
// that has none from VNDB, upload it into image_service (site=galgame_wiki), and
// pin it (galgame_cover sort_order=0, source="vndb", source_key=<cv-id>).
//
// Two image-byte sources share one upload/insert path:
//   - DUMP mode (Opts.VNDBImageDir set) — the one-time bulk backfill reads the
//     offline VNDB dump (db/vn → cover cv-id, db/images → rating, the rsync'd
//     cv/ tree → bytes). No VNDB API hammering — the established bulk technique.
//   - API mode (default) — the daily job fetches cover metadata via /vn for the
//     trickle of newly-claimed games and downloads the bytes from t.vndb.org.
//
// Candidate = published game (status=0) with a vndb_id and NO cover at all, so a
// user's own uploaded cover is never touched and re-runs only fill gaps. Writes
// the galgame_cover row only (no revision/snapshot) — same as the screenshot /
// banner backfills; the daily galgame-image-refping already pins historical
// hashes. CLI and scheduler run identical code from here.
package vndbcovers

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
	batchSize      = 100
	coverPreset    = "galgame_banner"
	defaultTimeout = 60 * time.Second
	vndbImageHost  = "https://t.vndb.org/"
)

// Opts selects the games to fill and where the cover bytes come from.
type Opts struct {
	Apply  bool          // false = dry run (resolve + count, no upload/insert)
	Gap    time.Duration // min delay between VNDB API calls (API mode; default 2s)
	IDs    []int         // process exactly these ids (any status); else published-only
	Limit  int
	Offset int

	// Dump mode (one-time bulk). When VNDBImageDir is set, cover bytes + cv-id +
	// ratings come from the offline dump instead of the VNDB API.
	VNDBImageDir   string // rsync mirror of rsync://dl.vndb.org/vndb-img (must contain cv/)
	VNDumpPath     string // dump's db/vn  (v-id -> cover cv-id)
	ImagesDumpPath string // dump's db/images (cv-id -> sexual/violence)

	ImageBaseURL string // optional image_service base override (else the client's configured base)
}

// coverMeta is the per-game cover descriptor both sources produce; the bytes are
// fetched separately (and only when applying) via fetchCover.
type coverMeta struct {
	cvID             string
	sexual, violence int16
}

// coverSource yields cover metadata per VN. The byte fetch is shared (fetchCover)
// because a VNDB cover URL is deterministic from its cv-id, so the only real
// difference between dump and API is where the metadata comes from.
type coverSource interface {
	prefetch(ctx context.Context, vndbIDs []string) error
	lookup(vndbID string) (coverMeta, bool)
}

// apiSource resolves cover metadata from the VNDB /vn API (daily trickle).
type apiSource struct {
	vc    *vndb.Client
	cache map[string]coverMeta
}

func newAPISource(gap time.Duration) *apiSource {
	return &apiSource{vc: vndb.New(gap), cache: map[string]coverMeta{}}
}

func (a *apiSource) prefetch(ctx context.Context, ids []string) error {
	m, err := a.vc.FetchVNImagesBatch(ctx, ids)
	if err != nil {
		return err
	}
	for id, img := range m {
		a.cache[id] = coverMeta{cvID: img.ID, sexual: ratingFloat(img.Sexual), violence: ratingFloat(img.Violence)}
	}
	return nil
}

func (a *apiSource) lookup(vndbID string) (coverMeta, bool) {
	m, ok := a.cache[vndbID]
	return m, ok
}

// ratingFloat maps a VNDB API 0-2 average flag vote onto our int16 0-2 column
// (reuses the dump rating's round-half-up + clamp by scaling to the 0-200 form).
func ratingFloat(avg float64) int16 { return rating(int(avg * 100)) }

// Run fills covers for the selected games and returns a summary.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.Gap <= 0 {
		opts.Gap = 2 * time.Second
	}

	// Covers live under site=galgame_wiki, and upload + reference-ping are
	// site-scoped. In the oauth scheduler container ImageClient is the *account*
	// client, so prefer the dedicated galgame client; fall back to ImageClient
	// (correct in the galgame container / CLI env-file). Mirror galgame-image-refping.
	clientCfg := cfg.ImageClient
	if cfg.GalgameImageClient.ClientID != "" && cfg.GalgameImageClient.ClientSecret != "" {
		clientCfg = cfg.GalgameImageClient
	}
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("galgame image client not configured (set KUN_GALGAME_IMAGE_CLIENT_ID/SECRET on the oauth container, or KUN_IMAGE_CLIENT_ID/SECRET); refusing to run")
	}

	// Build the metadata source: dump (one-time bulk) or API (daily trickle).
	var src coverSource
	mode := "api"
	if opts.VNDBImageDir != "" {
		mode = "dump"
		ds, err := newDumpSource(opts.VNDumpPath, opts.ImagesDumpPath)
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
		"apply", opts.Apply, "client_id", clientCfg.ClientID)

	var uploaded, noCover, wouldUpload, failed int
	var pingHashes []string
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), uploaded, noCover, wouldUpload, failed), err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		if err := src.prefetch(ctx, ids); err != nil {
			// VNDB down / out-of-range id: skip the batch, retry next run.
			failed += len(batch)
			slog.Error("prefetch batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "err", err)
			continue
		}

		for _, c := range batch {
			meta, ok := src.lookup(c.VNDBID)
			if !ok {
				noCover++ // VNDB has no cover for this VN
				continue
			}
			if !opts.Apply {
				wouldUpload++
				continue
			}
			body, err := fetchCover(httpClient, opts.VNDBImageDir, meta.cvID)
			if err != nil {
				failed++
				slog.Warn("fetch cover", "gid", c.ID, "cv", meta.cvID, "err", err)
				continue
			}
			hash, err := uploadAndInsert(ctx, db.DB(), cli, c.ID, body, meta)
			if err != nil {
				if stderrors.Is(err, imageclient.ErrQuotaExceeded) {
					slog.Error("upload quota exceeded — bump galgame_wiki image_quota_daily and re-run")
					return summary(len(cands), uploaded, noCover, wouldUpload, failed), err
				}
				failed++
				slog.Warn("upload+insert cover", "gid", c.ID, "cv", meta.cvID, "err", err)
				continue
			}
			uploaded++
			pingHashes = append(pingHashes, hash)
		}
		slog.Info("progress", "processed", end, "of", len(cands),
			"uploaded", uploaded, "no_cover", noCover, "failed", failed)
	}

	// Keep freshly-uploaded covers alive immediately (galgame-image-refping also
	// covers galgame_cover daily, but don't wait for it).
	if opts.Apply && len(pingHashes) > 0 {
		for _, b := range chunk(pingHashes, 1000) {
			if _, err := cli.ReferencePing(ctx, b); err != nil {
				slog.Warn("final reference-ping failed", "err", err)
			}
		}
	}

	slog.Info("sync-vndb-covers done", "candidates", len(cands), "uploaded", uploaded,
		"no_cover", noCover, "would_upload", wouldUpload, "failed", failed, "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), uploaded, noCover, wouldUpload, failed), nil
}

// uploadAndInsert uploads the cover bytes and pins the resulting hash. Returns
// the image hash. The INSERT is target-less ON CONFLICT DO NOTHING so it swallows
// BOTH unique constraints — (galgame_id,image_hash) and idx_galgame_cover_pinned
// (one sort_order=0 per game) — leaving a game that gained a cover between the
// candidate scan and now untouched.
func uploadAndInsert(ctx context.Context, db *gorm.DB, cli *imageclient.Client, gid int, body []byte, meta coverMeta) (string, error) {
	res, err := cli.Upload(ctx, bytes.NewReader(body), "cover.jpg", coverPreset)
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, created)
		 VALUES (?, ?, 0, ?, ?, 'vndb', ?, NOW())
		 ON CONFLICT DO NOTHING`,
		gid, res.Hash, meta.sexual, meta.violence, meta.cvID,
	).Error; err != nil {
		return "", fmt.Errorf("insert galgame_cover: %w", err)
	}
	return res.Hash, nil
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
		Where("NOT EXISTS (SELECT 1 FROM galgame_cover c WHERE c.galgame_id = galgame.id)").
		Order("id")
	if len(opts.IDs) > 0 {
		q = q.Where("id IN ?", opts.IDs) // targeted (any status)
	} else {
		q = q.Where("status = 0").Offset(opts.Offset)
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

func summary(cands, uploaded, noCover, wouldUpload, failed int) map[string]any {
	return map[string]any{
		"candidates":   cands,
		"uploaded":     uploaded,
		"no_cover":     noCover,
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
