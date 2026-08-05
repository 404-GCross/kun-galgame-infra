package getchuportraits

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"api/pkg/imageclient"

	"gorm.io/gorm"
)

type runner struct {
	db         *gorm.DB
	cli        *imageclient.Client
	slot       Slot
	gap        time.Duration
	stats      *Stats
	pingHashes []string
}

// charResult is one character's outcome. fill RETURNS it rather than mutating
// the runner, which is what makes the pool safe: every field is private to one
// character, so results merge serially on the driver goroutine and nothing is
// shared while uploads are in flight.
//
// The screenshot wave learned this the expensive way — a worker ranging a map
// the driver was assigning to is `concurrent map read and map write`, which is
// fatal, unrecoverable, and indifferent to the two touching different keys. It
// killed a production run at 8,077 uploads. Keeping worker-visible state to
// value types makes that unrepresentable rather than merely avoided.
type charResult struct {
	missing, uploaded, rejected, errors int
	quota                               bool
	hash                                string // fresh upload, for the reference ping
}

func (s *Stats) merge(r charResult) {
	s.Missing += r.missing
	s.Uploaded += r.uploaded
	s.Rejected += r.rejected
	s.Errors += r.errors
	if r.quota {
		s.Quota = true
	}
}

// fill uploads one character's bust and writes image_hash.
//
// The UPDATE re-asserts `image_hash IS NULL` even though the candidate was
// selected on it. Between selection and here another lane may have given this
// character a portrait, and this one is a fallback: losing that race must mean
// leaving the winner's image alone, not overwriting it. RowsAffected == 0 is
// therefore an ordinary outcome, not an error.
func (r *runner) fill(ctx context.Context, dir string, c candidate, apply bool) charResult {
	var out charResult
	path := mirrorPath(dir, c.GetchuID, c.File)
	if !fileExists(path) {
		out.missing++
		return out
	}
	if !apply {
		return out
	}

	res, err := r.upload(ctx, path, c.File)
	if err != nil {
		switch {
		case stderrors.Is(err, imageclient.ErrQuotaExceeded):
			slog.Warn("daily image quota exhausted — stopping", "character", c.CharacterID)
			out.quota = true
		case stderrors.Is(err, imageclient.ErrModerationRejected):
			out.rejected++
			slog.Warn("image rejected by moderation", "slot", r.slot.Name, "character", c.CharacterID, "getchu", c.GetchuID, "file", c.File)
		case stderrors.Is(err, imageclient.ErrMIMEDenied):
			// The source shipped a format this preset does not take (Getchu has
			// a handful of .gif character images). Counted as rejected, not an
			// error: nothing is broken and no re-run will change it.
			out.rejected++
			slog.Warn("format not accepted by preset", "slot", r.slot.Name, "character", c.CharacterID, "getchu", c.GetchuID, "file", c.File)
		default:
			out.errors++
			slog.Warn("upload character image", "slot", r.slot.Name, "character", c.CharacterID, "getchu", c.GetchuID, "file", c.File, "err", err)
		}
		return out
	}

	// Identifier interpolation, not user input — see loadPlates.
	tx := r.db.WithContext(ctx).Exec(fmt.Sprintf(
		`UPDATE catalog_character SET %[1]s = ?, updated_at = now()
		 WHERE id = ? AND %[1]s IS NULL AND deleted_at IS NULL`, r.slot.TargetColumn),
		res.Hash, c.CharacterID)
	if tx.Error != nil {
		out.errors++
		slog.Warn("write character image", "slot", r.slot.Name, "character", c.CharacterID, "err", tx.Error)
		return out
	}
	// Ping whether or not the row was claimed: the bytes are in the image
	// service either way and sit at TTL from upload time.
	out.hash = res.Hash
	if tx.RowsAffected > 0 {
		out.uploaded++
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
		res, err := r.cli.UploadWithSub(ctx, bytes.NewReader(body), filename, r.slot.Preset, r.slot.UploaderSub)
		if err == nil {
			return res, nil
		}
		// Quota, moderation and a rejected format are all TERMINAL verdicts —
		// retrying sends identical bytes for an identical answer.
		if stderrors.Is(err, imageclient.ErrQuotaExceeded) ||
			stderrors.Is(err, imageclient.ErrModerationRejected) ||
			stderrors.Is(err, imageclient.ErrMIMEDenied) {
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
// Unlike the screenshot lane there is nothing ordered to protect here: one
// character is one image and one row, so characters are independent and the
// only shared state is the result merge, which happens on the caller's
// goroutine. Stats and pingHashes stay single-writer and need no mutex.
func (r *runner) run(ctx context.Context, opts Opts, cands []candidate) {
	workers := opts.Workers
	if workers < 1 {
		workers = 1
	}
	if workers > len(cands) {
		workers = len(cands)
	}

	if workers <= 1 {
		for _, c := range cands {
			if ctx.Err() != nil || r.stats.Quota {
				return
			}
			r.absorb(r.fill(ctx, opts.MirrorDir, c, opts.Apply))
		}
		return
	}

	// Quota exhaustion is terminal for the whole run, so it cancels the shared
	// context and every in-flight worker stops at its next image.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	in := make(chan candidate)
	out := make(chan charResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range in {
				out <- r.fill(ctx, opts.MirrorDir, c, opts.Apply)
			}
		}()
	}
	go func() {
		defer close(in)
		for _, c := range cands {
			select {
			case <-ctx.Done():
				return
			case in <- c:
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()

	done := 0
	for res := range out {
		r.absorb(res)
		done++
		if res.quota {
			cancel()
		}
		if done%500 == 0 {
			slog.Info("getchu-portraits progress", "slot", r.slot.Name, "done", done, "of", len(cands),
				"uploaded", r.stats.Uploaded, "errors", r.stats.Errors)
		}
	}
}

// absorb folds one character's result into the run-level state. Single-writer
// by construction — only the driver goroutine calls it.
func (r *runner) absorb(res charResult) {
	r.stats.merge(res)
	if res.hash != "" {
		r.pingHashes = append(r.pingHashes, res.hash)
	}
}
