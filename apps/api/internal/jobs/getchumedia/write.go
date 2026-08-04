package getchumedia

import (
	"bytes"
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"time"

	"api/internal/platform/catalog/model"
	"api/pkg/imageclient"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// shotPreset is the image-service preset for catalog screenshots
	// (configs/image_presets.yaml). The catalog image client's
	// image_allowed_presets MUST contain it or every upload is 403'd.
	shotPreset = "catalog_screenshot"
	// uploaderSub stamps a machine identity on first_uploader_sub — there is no
	// human uploader behind a batch backfill, and the audit trail should say so.
	uploaderSub = "system:getchu-media-backfill"
	// uploadRetries rides out an image-container recreation mid-run (~30-90s
	// unreachable). Quota and moderation are terminal and never retried.
	uploadRetries  = 6
	defaultTimeout = 60 * time.Second
)

type runner struct {
	db         *gorm.DB
	cli        *imageclient.Client
	source     int16
	have       map[int64]map[string]bool // work → image hashes already present
	gap        time.Duration
	maxPerWork int
	stats      *Stats
	touched    []int64
	pingHashes []string
}

// fill uploads one work's Getchu sample CG and writes its screenshot rows.
//
// A work can carry several Getchu releases (a regular edition, a DL edition);
// their sample sets are concatenated in anchor order, and sort_order is the
// running position across the whole gallery rather than each release's own
// index — two releases both starting at 0 would otherwise interleave into
// nonsense. Duplicate bytes across the two collapse on the (work, hash) key.
func (r *runner) fill(ctx context.Context, dir string, c candidate, staged map[string][]stagedImage, apply bool) {
	var images []stagedImage
	for _, gid := range c.GetchuIDs {
		images = append(images, staged[gid]...)
	}
	if len(images) == 0 {
		r.stats.NoStaged++
		return
	}
	if r.maxPerWork > 0 && len(images) > r.maxPerWork {
		images = images[:r.maxPerWork]
	}

	sort := 0
	for _, im := range images {
		if ctx.Err() != nil {
			return
		}
		path := mirrorPath(dir, im.GetchuID, im.File)
		if !fileExists(path) {
			r.stats.Missing++
			continue
		}
		r.stats.Planned++
		if !apply {
			sort++
			continue
		}

		res, err := r.upload(ctx, path, im.File)
		if err != nil {
			if r.classify(err, c.WorkID, im) {
				r.stats.Quota = true
				return
			}
			continue
		}
		if r.have[c.WorkID][res.Hash] {
			r.stats.Dedup++
			continue
		}
		tx := r.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "work_id"}, {Name: "image_hash"}},
			DoNothing: true,
		}).Create(&model.CatalogWorkScreenshot{
			WorkID: c.WorkID, ImageHash: res.Hash, SortOrder: sort, Caption: "",
			// The rating comes from the WORK, not from the image or the store
			// page: catalog_work.content_rating is the value the read face
			// already gates on, and both scales are the same 0/1/2. Deriving a
			// per-image guess here would let a gallery disagree with the work
			// it hangs on.
			Sexual: c.ContentRating, Violence: 0, SourceID: r.source,
		})
		if tx.Error != nil {
			r.stats.Errors++
			slog.Warn("write screenshot row", "work", c.WorkID, "getchu", im.GetchuID, "err", tx.Error)
			continue
		}
		// Ping regardless of whether the row was new: the bytes are in the
		// image service either way and sit at TTL from upload time.
		r.pingHashes = append(r.pingHashes, res.Hash)
		if tx.RowsAffected == 0 {
			r.stats.Dedup++
			continue
		}
		set := r.have[c.WorkID]
		if set == nil {
			set = map[string]bool{}
			r.have[c.WorkID] = set
		}
		set[res.Hash] = true
		r.touched = append(r.touched, c.WorkID)
		r.stats.Uploaded++
		sort++
	}
}

// upload reads a mirrored image and uploads it. Bytes only ever come from the
// local mirror — this never dials getchu.com.
func (r *runner) upload(ctx context.Context, path, filename string) (*imageclient.UploadResult, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, os.ErrNotExist
	}
	var lastErr error
	for attempt := 0; attempt < uploadRetries; attempt++ {
		if r.gap > 0 {
			time.Sleep(r.gap)
		}
		// A fresh reader per attempt — the previous one is consumed.
		res, err := r.cli.UploadWithSub(ctx, bytes.NewReader(body), filename, shotPreset, uploaderSub)
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

// classify maps an upload error to a counter. Only quota exhaustion stops the
// run — a moderation rejection is that one image's verdict, not the batch's.
func (r *runner) classify(err error, workID int64, im stagedImage) (quota bool) {
	switch {
	case stderrors.Is(err, imageclient.ErrQuotaExceeded):
		slog.Warn("daily image quota exhausted — stopping", "work", workID)
		return true
	case stderrors.Is(err, imageclient.ErrModerationRejected):
		r.stats.Rejected++
		slog.Warn("screenshot rejected by moderation", "work", workID, "getchu", im.GetchuID, "file", im.File)
		return false
	default:
		r.stats.Errors++
		slog.Warn("upload screenshot", "work", workID, "getchu", im.GetchuID, "file", im.File, "err", err)
		return false
	}
}

// ping keeps freshly-uploaded bytes alive immediately. An image sits at TTL
// from upload time, so waiting for the nightly refping is a real risk of
// uploading bytes and then losing them.
func (r *runner) ping(ctx context.Context) error {
	if r.cli == nil || len(r.pingHashes) == 0 {
		return nil
	}
	for i := 0; i < len(r.pingHashes); i += 1000 {
		batch := r.pingHashes[i:min(i+1000, len(r.pingHashes))]
		if _, err := r.cli.ReferencePing(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
