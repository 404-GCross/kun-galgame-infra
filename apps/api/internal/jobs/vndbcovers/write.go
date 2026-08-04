package vndbcovers

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"time"

	"api/internal/platform/catalog/model"
	"api/pkg/imageclient"

	"gorm.io/gorm/clause"
)

const (
	// coverPreset is the image-service preset these uploads use
	// (configs/image_presets.yaml). The catalog image client's
	// image_allowed_presets already contains it — the DLsite (step 55) and
	// Bangumi cover lanes upload through the same client under the same preset,
	// so this wave needs no preset change.
	coverPreset = "catalog_cover"

	// uploaderSub stamps a machine identity onto first_uploader_sub so the
	// backfilled image rows are traceable — there is no human uploader.
	uploaderSub = "system:vndb-cover-backfill"

	// coverKind is the VNDB-native cover kind. catalog_work_cover.kind already
	// speaks VNDB's vocabulary (main / pkgfront / dig / …) and /vn's `image` is
	// by definition the vn's main cover.
	coverKind = "main"

	// uploadRetries rides out an image-container recreation mid-run (~30-90s
	// unreachable, breaking in-flight connections). Quota and moderation are
	// terminal and never retried. Matches the step-55 machinery.
	uploadRetries = 6

	// downloadRetries bounds the retries of a transient t.vndb.org failure.
	downloadRetries = 3

	// maxImageBytes refuses an absurd payload before it reaches memory. VNDB
	// covers are a few hundred KB; 16 MiB is a fuse, not a limit anyone hits.
	maxImageBytes = 16 << 20

	downloadTimeout = 60 * time.Second

	// defaultCoverFilename names an upload whose URL carries no usable basename.
	defaultCoverFilename = "cover.jpg"
)

// fill downloads one work's VNDB cover and writes its catalog_work_cover row.
//
// portrait_pinned follows the cover's own shape (h > w): VNDB covers are mostly
// vertical package art, which is exactly what the portrait-first UI wants
// pinned, but the landscape ones must not claim the pin. sexual/violence come
// from VNDB's own per-image votes rounded onto the catalog scale (ratingLevel)
// — this is the one source in the media lanes that grades the IMAGE rather than
// the work, so the flags need no work-level fallback.
func (r *runner) fill(ctx context.Context, row planRow) {
	body, filename, err := r.download(ctx, row.Img.URL)
	if err != nil {
		r.stats.Errors++
		slog.Warn("download vndb cover", "work", row.WorkID, "vn", row.VNDBID, "url", row.Img.URL, "err", err)
		return
	}
	res, err := r.upload(ctx, body, filename)
	if err != nil {
		if r.classify(err, row) {
			r.stats.Quota = true
		}
		return
	}
	tx := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "image_hash"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkCover{
		WorkID: row.WorkID, ImageHash: res.Hash, SortOrder: 0, Kind: coverKind,
		PortraitPinned: portrait(row.Img.Dims),
		Sexual:         ratingLevel(row.Img.Sexual),
		Violence:       ratingLevel(row.Img.Violence),
		SourceID:       r.sourceID,
	})
	if tx.Error != nil {
		r.stats.Errors++
		slog.Warn("write vndb cover row", "work", row.WorkID, "vn", row.VNDBID, "err", tx.Error)
		return
	}
	// Ping regardless of whether the row was new: the bytes are in the image
	// service either way and sit at TTL from upload time.
	r.pingHashes = append(r.pingHashes, res.Hash)
	if tx.RowsAffected == 0 {
		r.stats.Dedup++
		return
	}
	r.touched = append(r.touched, row.WorkID)
	r.stats.Uploaded++
}

// download fetches the cover bytes from t.vndb.org, returning the body and the
// filename to upload it under (the CDN basename, which carries the extension).
func (r *runner) download(ctx context.Context, src string) ([]byte, string, error) {
	client := &http.Client{Timeout: downloadTimeout}
	var lastErr error
	for attempt := 0; attempt < downloadRetries; attempt++ {
		if attempt > 0 && !sleepCtx(ctx, time.Duration(5<<attempt)*time.Second) {
			return nil, "", ctx.Err()
		}
		body, err := fetch(ctx, client, src)
		if err == nil {
			return body, coverFilename(src), nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
	}
	return nil, "", lastErr
}

func fetch(ctx context.Context, client *http.Client, src string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image cdn %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("image cdn returned an empty body")
	}
	if len(body) > maxImageBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageBytes)
	}
	return body, nil
}

// coverFilename is the upload filename for a t.vndb.org URL — the basename of
// its PATH (e.g. "17.jpg"), which carries the extension the image service
// sniffs against. A URL with no usable path component falls back to a generic
// name rather than to the host.
func coverFilename(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return defaultCoverFilename
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" || path.Ext(base) == "" {
		return defaultCoverFilename
	}
	return base
}

// upload pushes the downloaded bytes under the catalog_cover preset, retrying
// transient image-service failures with a fresh reader per attempt (the
// previous one is consumed). Terminal errors (quota / moderation) return at
// once.
func (r *runner) upload(ctx context.Context, body []byte, filename string) (*imageclient.UploadResult, error) {
	var lastErr error
	for attempt := 0; attempt < uploadRetries; attempt++ {
		if r.gap > 0 && !sleepCtx(ctx, r.gap) {
			return nil, ctx.Err()
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
		if attempt < uploadRetries-1 && !sleepCtx(ctx, time.Duration(min(5<<attempt, 30))*time.Second) {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// classify maps an upload error to a counter. Only quota exhaustion stops the
// run — a moderation rejection is that one cover's verdict, not the batch's.
func (r *runner) classify(err error, row planRow) (quota bool) {
	switch {
	case stderrors.Is(err, imageclient.ErrQuotaExceeded):
		slog.Warn("daily image quota exhausted — stopping", "work", row.WorkID)
		return true
	case stderrors.Is(err, imageclient.ErrModerationRejected):
		r.stats.Rejected++
		slog.Warn("vndb cover rejected by moderation", "work", row.WorkID, "vn", row.VNDBID)
		return false
	default:
		r.stats.Errors++
		slog.Warn("upload vndb cover", "work", row.WorkID, "vn", row.VNDBID, "err", err)
		return false
	}
}
