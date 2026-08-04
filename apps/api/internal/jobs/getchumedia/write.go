package getchumedia

import (
	"bytes"
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"sync"
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

// workResult is one work's outcome. fill returns it instead of mutating the
// runner, which is what lets works run CONCURRENTLY: every field here is
// private to one work, so the pool merges results serially and nothing is
// shared while the uploads are in flight.
type workResult struct {
	dedup, missing, planned, uploaded, errors, rejected int
	noStaged, quota                                     bool
	hashes                                              []string // fresh uploads, for the reference ping
	touched                                             bool     // the work gained at least one row
}

func (s *Stats) merge(r workResult) {
	s.Dedup += r.dedup
	s.Missing += r.missing
	s.Planned += r.planned
	s.Uploaded += r.uploaded
	s.Errors += r.errors
	s.Rejected += r.rejected
	if r.noStaged {
		s.NoStaged++
	}
	if r.quota {
		s.Quota = true
	}
}

// fill uploads one work's Getchu sample CG and writes its screenshot rows.
//
// A work can carry several Getchu releases (a regular edition, a DL edition);
// their sample sets are concatenated in anchor order, and sort_order is the
// running position across the whole gallery rather than each release's own
// index — two releases both starting at 0 would otherwise interleave into
// nonsense. That is also why the pool parallelises across WORKS and never
// within one: sort_order is assigned by this loop's position, so two goroutines
// inside one gallery would scramble it.
//
// `present` is passed IN rather than read from runner.have. That is not style:
// runner.have is one shared Go map, and a worker ranging over it while the
// driver assigns a key is `concurrent map read and map write` — fatal, and
// indifferent to the fact that the two touch different works. This crashed a
// production run at 8,077 uploads. Handing the worker its own set means no
// worker touches a shared map at all, which is a property of the signature
// rather than a rule someone has to keep remembering.
func (r *runner) fill(ctx context.Context, dir string, c candidate, present map[string]bool, staged map[string][]stagedImage, apply bool) workResult {
	var out workResult
	var images []stagedImage
	seenBytes := map[string]bool{}
	for _, gid := range c.GetchuIDs {
		for _, im := range staged[gid] {
			// A work's Getchu editions (regular / DL) publish the SAME CG set, so
			// concatenating their samples offers the same bytes twice. The
			// (work_id, image_hash) key would collapse them anyway — but only
			// AFTER paying ~2s to upload each duplicate, and across the backfill
			// that is 2,556 of 16,199 uploads spent to learn nothing. The mirror
			// already recorded each file's sha256, so the duplicate is knowable
			// here for free. Files with no recorded hash fall through and are
			// uploaded, where the unique key still catches them.
			if im.SHA256 != "" {
				if seenBytes[im.SHA256] {
					out.dedup++
					continue
				}
				seenBytes[im.SHA256] = true
			}
			images = append(images, im)
		}
	}
	if len(images) == 0 {
		out.noStaged = true
		return out
	}
	if r.maxPerWork > 0 && len(images) > r.maxPerWork {
		images = images[:r.maxPerWork]
	}
	sort := 0
	for _, im := range images {
		if ctx.Err() != nil {
			return out
		}
		path := mirrorPath(dir, im.GetchuID, im.File)
		if !fileExists(path) {
			out.missing++
			continue
		}
		out.planned++
		if !apply {
			sort++
			continue
		}

		res, err := r.upload(ctx, path, im.File)
		if err != nil {
			if r.classify(err, c.WorkID, im, &out) {
				out.quota = true
				return out
			}
			continue
		}
		if present[res.Hash] {
			out.dedup++
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
			out.errors++
			slog.Warn("write screenshot row", "work", c.WorkID, "getchu", im.GetchuID, "err", tx.Error)
			continue
		}
		// Ping regardless of whether the row was new: the bytes are in the
		// image service either way and sit at TTL from upload time.
		out.hashes = append(out.hashes, res.Hash)
		if tx.RowsAffected == 0 {
			out.dedup++
			continue
		}
		present[res.Hash] = true
		out.touched = true
		out.uploaded++
		sort++
	}
	return out
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
func (r *runner) classify(err error, workID int64, im stagedImage, out *workResult) (quota bool) {
	switch {
	case stderrors.Is(err, imageclient.ErrQuotaExceeded):
		slog.Warn("daily image quota exhausted — stopping", "work", workID)
		return true
	case stderrors.Is(err, imageclient.ErrModerationRejected):
		out.rejected++
		slog.Warn("screenshot rejected by moderation", "work", workID, "getchu", im.GetchuID, "file", im.File)
		return false
	default:
		out.errors++
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

// run walks the candidates through a fixed pool of workers.
//
// The pool is across WORKS, never inside one: sort_order is assigned by fill's
// own loop position, so two goroutines in one gallery would scramble it, and
// the bytes-already-sent check is per-work state. One work per worker keeps
// both correct without a lock on either.
//
// Concurrency is worth having because the bottleneck is not ours: at one upload
// at a time the image service answered in ~2s while sitting at 5% CPU — the
// time is the object store round trip, so the fix is to have several in flight,
// not to make each faster. A serial pass over this backfill is ~7.5 hours.
//
// Results merge on the caller's goroutine as they arrive, so Stats, touched and
// pingHashes are still single-writer and need no mutex.
func (r *runner) run(ctx context.Context, opts Opts, cands []candidate, staged map[string][]stagedImage) {
	workers := opts.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(cands) {
		workers = len(cands)
	}
	// Each work's already-present hashes are snapshotted HERE, on one
	// goroutine, and handed to the worker by value — runner.have is never
	// reached from a worker. See fill.
	snapshot := func(workID int64) map[string]bool {
		present := make(map[string]bool, len(r.have[workID]))
		for h := range r.have[workID] {
			present[h] = true
		}
		return present
	}

	if workers <= 1 {
		for _, c := range cands {
			if ctx.Err() != nil || r.stats.Quota {
				return
			}
			r.absorb(c.WorkID, r.fill(ctx, opts.MirrorDir, c, snapshot(c.WorkID), staged, opts.Apply))
		}
		return
	}

	// Quota exhaustion is terminal for the whole run, so it cancels the shared
	// context and every in-flight worker stops at its next image.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The feeder pairs each candidate with its snapshot, so the snapshot is
	// taken on the feeder goroutine and read only by the worker that receives it.
	type task struct {
		c       candidate
		present map[string]bool
	}
	type job struct {
		c   candidate
		res workResult
	}
	in := make(chan task)
	out := make(chan job, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range in {
				out <- job{c: t.c, res: r.fill(ctx, opts.MirrorDir, t.c, t.present, staged, opts.Apply)}
			}
		}()
	}
	go func() {
		defer close(in)
		for _, c := range cands {
			select {
			case <-ctx.Done():
				return
			case in <- task{c: c, present: snapshot(c.WorkID)}:
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()

	done := 0
	for j := range out {
		r.absorb(j.c.WorkID, j.res)
		done++
		if j.res.quota {
			cancel()
		}
		if done%200 == 0 {
			slog.Info("getchu-media progress", "works_done", done, "of", len(cands),
				"uploaded", r.stats.Uploaded, "errors", r.stats.Errors)
		}
	}
}

// absorb folds one work's result into the run-level state. Single-writer by
// construction — only the driver goroutine calls it.
//
// It deliberately does NOT write the fresh hashes back into runner.have. Each
// work is dispatched exactly once, so nothing would ever read them again, and
// the write was what turned runner.have into a map being mutated while workers
// ranged over it.
func (r *runner) absorb(workID int64, res workResult) {
	r.stats.merge(res)
	r.pingHashes = append(r.pingHashes, res.hashes...)
	if res.touched {
		r.touched = append(r.touched, workID)
	}
}
