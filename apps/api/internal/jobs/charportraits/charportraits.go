// Package charportraits backfills catalog CHARACTER portraits: it reads the
// step-47 output src_vndb.portrait_backfill (one in-gate VNDB character portrait
// per catalog character, already filtered to sexual/violence ≤ 100), uploads the
// portrait bytes from a local VNDB image mirror (rsync ch/) into the image
// service under site_key "catalog" (preset "character"), and writes the returned
// content hash back to catalog_character.image_hash.
//
// It is the character-layer sibling of internal/jobs/portraitfill (which fills
// galgame COVER portraits under site galgame_wiki). Design (refs/proj/48):
//
//   - Idempotent: a character whose image_hash is already non-null is SKIPPED
//     before any byte read (skipped_has_hash). A second full run writes nothing.
//   - Bytes only ever come from a LOCAL mirror dir (--vndb-image-dir). rsync is
//     the ops fetch step; this job never dials t.vndb.org.
//   - Catalog DSN is ALWAYS explicit (--dsn): the rehearsal copy locally
//     (kun_catalog_rehearsal), the live catalog only in the acceptance-tester's
//     production run — it is NEVER defaulted, so a bare run cannot touch a live DB.
//   - image_hash is written under site "catalog"; the sibling catalog portrait
//     refping (internal/jobs.RunCatalogImageRefping) keeps that hash set alive.
package charportraits

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	// preset is the image-service preset for character portraits (see
	// apps/api/configs/image_presets.yaml). The catalog image client's
	// image_allowed_presets MUST contain it, else every upload is 403'd.
	preset = "character"
	// uploaderSub stamps a machine identity onto first_uploader_sub so the
	// backfilled rows are traceable in the image audit trail (there is no human
	// uploader for a batch backfill).
	uploaderSub = "system:catalog-portrait-backfill"
	// site is the image tenant these portraits belong to (informational; the
	// actual site is derived server-side from the authenticated client's
	// image_site_key, which must equal "catalog").
	site           = "catalog"
	defaultTimeout = 60 * time.Second
)

// Opts configures a run.
type Opts struct {
	Apply        bool          // false = dry run: resolve candidates + count local-file availability, upload nothing
	Limit        int           // max portrait_backfill rows to process (0 = all)
	Offset       int           // skip this many rows (for chunking)
	DSN          string        // catalog DSN — REQUIRED; rehearsal copy locally, live catalog only in the prod run
	VNDBImageDir string        // local rsync mirror root containing ch/ (bytes are read from here, never fetched)
	ImageBaseURL string        // image_service base override (point at the LOCAL compose/dev service)
	UploadGap    time.Duration // min delay between uploads (0 = none; raise for the production sweep)
}

// candidate is one portrait_backfill row joined to its catalog character's
// current image_hash (nil pointer = not yet backfilled).
type candidate struct {
	CatalogCharacterID int64   `gorm:"column:catalog_character_id"`
	ImageID            string  `gorm:"column:image_id"`
	ImageHash          *string `gorm:"column:image_hash"`
}

// runner carries per-run dependencies + counters (serial, so plain ints).
type runner struct {
	db  *gorm.DB
	cli *imageclient.Client
	gap time.Duration

	uploaded, skippedHasHash, missingFile, rejected, errors, dedup int
	pingHashes                                                     []string
}

// Run resolves the backfill set, forecasts local-file availability, and (when
// Apply) uploads portraits + writes image_hash. Returns a loggable summary.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the production run")
	}
	if opts.VNDBImageDir == "" {
		return nil, fmt.Errorf("--vndb-image-dir is required (local rsync mirror containing ch/)")
	}

	// Catalog is its own image tenant. Unlike galgame_wiki there is no account
	// fallback: the client MUST be site_key "catalog".
	clientCfg := cfg.CatalogImageClient
	if opts.Apply && (clientCfg.ClientID == "" || clientCfg.ClientSecret == "") {
		return nil, fmt.Errorf("catalog image client not configured (set KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to --apply")
	}

	db, err := database.OpenJob(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	cands, err := loadCandidates(ctx, db, opts.Limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	slog.Info("char-portraits candidates", "candidates", len(cands), "apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)

	r := &runner{db: db, gap: opts.UploadGap}

	// ---- dry run: forecast only (no client, no writes) ----
	if !opts.Apply {
		var hasHash, present, missing, badID int
		for _, c := range cands {
			if c.ImageHash != nil && *c.ImageHash != "" {
				hasHash++
				continue
			}
			rel, perr := chRelPath(c.ImageID)
			if perr != nil {
				badID++
				continue
			}
			if fileExists(filepath.Join(opts.VNDBImageDir, filepath.FromSlash(rel))) {
				present++
			} else {
				missing++
			}
		}
		slog.Info("char-portraits DRY forecast",
			"candidates", len(cands), "skipped_has_hash", hasHash,
			"local_present", present, "missing_file", missing, "bad_id", badID)
		return map[string]any{
			"apply":            false,
			"candidates":       len(cands),
			"skipped_has_hash": hasHash,
			"local_present":    present,
			"missing_file":     missing,
			"bad_id":           badID,
		}, nil
	}

	// ---- apply ----
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
			return r.summary(len(cands)), err
		}
		if c.ImageHash != nil && *c.ImageHash != "" {
			r.skippedHasHash++
			continue
		}
		if r.fillPortrait(ctx, opts.VNDBImageDir, c) {
			quota = true
			break
		}
	}

	// Keep freshly-uploaded portraits alive immediately (don't wait for the
	// nightly refping — an image sits at TTL from upload time).
	for _, b := range chunk(r.pingHashes, 1000) {
		if _, err := r.cli.ReferencePing(ctx, b); err != nil {
			slog.Warn("char-portraits reference-ping failed", "err", err)
		}
	}

	slog.Info("char-portraits done",
		"uploaded", r.uploaded, "skipped_has_hash", r.skippedHasHash,
		"missing_file", r.missingFile, "rejected", r.rejected, "errors", r.errors, "dedup", r.dedup)
	if quota {
		return r.summary(len(cands)), fmt.Errorf("image quota exceeded after %d uploads", r.uploaded)
	}
	return r.summary(len(cands)), nil
}

// fillPortrait reads one local portrait, uploads it, and writes image_hash.
// Returns quota=true when the daily image quota is exhausted (caller aborts).
func (r *runner) fillPortrait(ctx context.Context, dir string, c candidate) (quota bool) {
	rel, err := chRelPath(c.ImageID)
	if err != nil {
		r.errors++
		slog.Warn("bad image id", "char", c.CatalogCharacterID, "image_id", c.ImageID, "err", err)
		return false
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			r.missingFile++
			return false
		}
		r.errors++
		slog.Warn("read portrait", "char", c.CatalogCharacterID, "rel", rel, "err", err)
		return false
	}
	if len(body) == 0 {
		r.missingFile++
		return false
	}
	if r.gap > 0 {
		time.Sleep(r.gap)
	}
	res, err := r.cli.UploadWithSub(ctx, bytes.NewReader(body), c.ImageID+".jpg", preset, uploaderSub)
	if err != nil {
		switch {
		case stderrors.Is(err, imageclient.ErrQuotaExceeded):
			return true
		case stderrors.Is(err, imageclient.ErrModerationRejected):
			r.rejected++
			slog.Warn("portrait rejected by moderation", "char", c.CatalogCharacterID, "image_id", c.ImageID, "err", err)
			return false
		default:
			r.errors++
			slog.Warn("upload portrait", "char", c.CatalogCharacterID, "image_id", c.ImageID, "err", err)
			return false
		}
	}
	if res.Deduplicated {
		r.dedup++
	}
	// Write only when still unset — a concurrent run (or a portrait shared by
	// several characters mid-batch) never clobbers, and this is the idempotency
	// key: a re-run reads image_hash non-null and skips before uploading.
	tx := r.db.WithContext(ctx).Exec(
		`UPDATE catalog_character SET image_hash = ?, updated_at = NOW() WHERE id = ? AND image_hash IS NULL`,
		res.Hash, c.CatalogCharacterID)
	if tx.Error != nil {
		r.errors++
		slog.Warn("write image_hash", "char", c.CatalogCharacterID, "err", tx.Error)
		return false
	}
	if tx.RowsAffected == 0 {
		// Already set by a concurrent writer — count as a skip, not an upload.
		r.skippedHasHash++
		return false
	}
	r.uploaded++
	r.pingHashes = append(r.pingHashes, res.Hash)
	return false
}

func (r *runner) summary(candidates int) map[string]any {
	return map[string]any{
		"apply":            true,
		"candidates":       candidates,
		"uploaded":         r.uploaded,
		"skipped_has_hash": r.skippedHasHash,
		"missing_file":     r.missingFile,
		"rejected":         r.rejected,
		"errors":           r.errors,
		"dedup":            r.dedup,
	}
}

// loadCandidates reads the backfill set joined to each character's current
// image_hash, ordered + windowed for chunking. image_hash is fetched (not
// filtered) so a re-run reports skipped_has_hash for the whole window.
func loadCandidates(ctx context.Context, db *gorm.DB, limit, offset int) ([]candidate, error) {
	q := db.WithContext(ctx).
		Table("src_vndb.portrait_backfill AS pb").
		Select("pb.catalog_character_id, pb.image_id, c.image_hash").
		Joins("JOIN catalog_character c ON c.id = pb.catalog_character_id AND c.deleted_at IS NULL").
		Order("pb.catalog_character_id")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	var out []candidate
	if err := q.Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
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

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
