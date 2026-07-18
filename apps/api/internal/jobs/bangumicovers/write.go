package bangumicovers

import (
	"bytes"
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"api/internal/platform/catalog/model"
	"api/pkg/imageclient"

	"gorm.io/gorm/clause"
)

const (
	// coverPreset is the image-service preset these uploads use
	// (configs/image_presets.yaml). The catalog image client's
	// image_allowed_presets already contains it (the step-55 DLsite cover
	// backfill uses the same preset over the same client) — no new preset.
	coverPreset = "catalog_cover"

	// uploaderSub stamps a machine identity onto first_uploader_sub so the
	// backfilled image rows are traceable (there is no human uploader).
	uploaderSub = "system:bangumi-cover-backfill"

	// uploadRetries is how many times upload() retries a TRANSIENT image-service
	// failure (connection refused / timeout / 5xx). The infra stack redeploys
	// mid-run and recreates the image container (~30-90s unreachable, breaking
	// in-flight connections); without retry those uploads are permanently skipped
	// for the pass. Quota/moderation are terminal and never retried. Matches the
	// step-55 machinery (internal/jobs/dlsitemedia).
	uploadRetries = 6
)

// writeCover uploads a bodyless work's vertical Bangumi PORTRAIT cover from the
// mirror and writes one catalog_work_cover row with portrait_pinned=true — the
// value kungal/moyu read for the portrait-first UI. Returns quota=true when the
// daily image quota is exhausted (caller aborts). The caller has already
// classified this candidate as portrait (h > w) via dims; landscape/square
// covers never reach here.
//
// Sexual is fixed at 0: Bangumi has no per-image content rating, and its
// subject-level nsfw flag is not consulted (this tool is catalog + local-mirror
// only). Bangumi covers are store/catalog images and are generally SFW, so 0 is
// the conservative-yet-correct default (§任务, spec default). Violence is always 0.
func (r *runner) writeCover(ctx context.Context, mirrorRoot string, c candidate, e dimsEntry, apply bool) (quota bool) {
	if !isBodyless(c.Site) { // XOR guard (§8.D) — never materialise a claimed work
		r.c.coverRefused++
		return false
	}
	if r.exist[c.WorkID] { // preloaded Bangumi cover → skip before any byte read
		r.c.coverExists++
		return false
	}
	path := coverPath(mirrorRoot, c.SubjectID, e)
	if !fileExists(path) {
		r.c.coverMissing++
		return false
	}
	if !apply {
		r.c.coverWould++
		return false
	}
	res, upErr := r.upload(ctx, path)
	if upErr != nil {
		return r.classifyUpload(upErr, c)
	}
	tx := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "image_hash"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkCover{
		WorkID: c.WorkID, ImageHash: res.Hash, SortOrder: 0, Kind: "main",
		PortraitPinned: true, Sexual: 0, Violence: 0, SourceID: r.sourceID,
	})
	if tx.Error != nil {
		r.c.errors++
		slog.Warn("write bangumi cover row", "work", c.WorkID, "subject", c.SubjectID, "err", tx.Error)
		return false
	}
	r.pingHashes = append(r.pingHashes, res.Hash) // keep the byte alive immediately
	if tx.RowsAffected == 0 {
		r.c.coverDedup++
		return false
	}
	r.exist[c.WorkID] = true
	r.c.coverUploaded++
	return false
}

// upload reads a mirrored cover and uploads it under the catalog_cover preset.
// The bytes only ever come from the local mirror — this never dials Bangumi.
// Retries transient failures across an image-container recreation with a fresh
// bytes.Reader per attempt (the previous one is consumed); terminal errors
// (quota / moderation) return immediately. Copied from the step-55 machinery so
// the retry/backoff shape is identical, per the "match those patterns" contract.
func (r *runner) upload(ctx context.Context, path string) (*imageclient.UploadResult, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, os.ErrNotExist
	}
	filename := filepath.Base(path)
	var lastErr error
	for attempt := 0; attempt < uploadRetries; attempt++ {
		if r.gap > 0 {
			time.Sleep(r.gap)
		}
		res, err := r.cli.UploadWithSub(ctx, bytes.NewReader(body), filename, coverPreset, uploaderSub)
		if err == nil {
			return res, nil
		}
		if stderrors.Is(err, imageclient.ErrQuotaExceeded) || stderrors.Is(err, imageclient.ErrModerationRejected) {
			return nil, err
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt < uploadRetries-1 {
			time.Sleep(time.Duration(min(5<<attempt, 30)) * time.Second)
		}
	}
	return nil, lastErr
}

// classifyUpload maps an upload error to a counter. Returns quota=true only for
// ErrQuotaExceeded (the caller aborts the whole run); moderation rejection is
// counted and the run continues; any other error (incl. a vanished file) counts
// as a generic error.
func (r *runner) classifyUpload(err error, c candidate) (quota bool) {
	switch {
	case stderrors.Is(err, imageclient.ErrQuotaExceeded):
		return true
	case stderrors.Is(err, imageclient.ErrModerationRejected):
		r.c.coverRejected++
		slog.Warn("bangumi cover rejected by moderation", "work", c.WorkID, "subject", c.SubjectID)
		return false
	default:
		r.c.errors++
		slog.Warn("upload bangumi cover", "work", c.WorkID, "subject", c.SubjectID, "err", err)
		return false
	}
}
