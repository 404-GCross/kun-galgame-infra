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
	shotPreset     = "catalog_screenshot"
	uploaderSub    = "system:getchu-media-backfill"
	uploadRetries  = 6
	defaultTimeout = 60 * time.Second
)

type runner struct {
	db         *gorm.DB
	cli        *imageclient.Client
	source     int16
	have       map[int64]map[string]bool
	gap        time.Duration
	maxPerWork int
	stats      *Stats
	touched    []int64
	pingHashes []string
}

type workResult struct {
	dedup, missing, planned, uploaded, errors, rejected int
	noStaged, quota                                     bool
	hashes                                              []string
	touched                                             bool
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

func (r *runner) fill(ctx context.Context, dir string, c candidate, present map[string]bool, staged map[string][]stagedImage, apply bool) workResult {
	var out workResult
	var images []stagedImage
	seenBytes := map[string]bool{}
	for _, gid := range c.GetchuIDs {
		for _, im := range staged[gid] {
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
			Sexual: c.ContentRating, Violence: 0, SourceID: r.source,
		})
		if tx.Error != nil {
			out.errors++
			slog.Warn("write screenshot row", "work", c.WorkID, "getchu", im.GetchuID, "err", tx.Error)
			continue
		}
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

func (r *runner) run(ctx context.Context, opts Opts, cands []candidate, staged map[string][]stagedImage) {
	workers := opts.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(cands) {
		workers = len(cands)
	}
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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

func (r *runner) absorb(workID int64, res workResult) {
	r.stats.merge(res)
	r.pingHashes = append(r.pingHashes, res.hashes...)
	if res.touched {
		r.touched = append(r.touched, workID)
	}
}
