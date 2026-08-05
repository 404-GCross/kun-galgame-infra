package labellogos

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"api/pkg/imageclient"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// logoPreset is the image-service preset these uploads use
// (configs/image_presets.yaml). It is `inside`-fit, not `cover`: a wordmark
// cropped to a square box loses the brand's own name. The catalog client's
// image_allowed_presets MUST list it or every upload is 403'd.
const logoPreset = "catalog_logo"

// provField is the field_provenance key this lane writes under — the column
// name, matching every other writer in the catalog.
const provField = "logo_hash"

// provEntry is one provenance record, R8 array shape
// ({"<field>":[{"source","at"}, ...]}, latest first) — byte-identical to the
// charattrs / orglabels / personmint writers.
type provEntry struct {
	Source string `json:"source"`
	At     string `json:"at"`
}

// imageUploader is the slice of the image client this lane needs. Narrowing to
// an interface (satisfied by *imageclient.Client) lets the write path be
// exercised with a fake in tests — no image service, no network.
type imageUploader interface {
	UploadWithSub(ctx context.Context, r io.Reader, filename, preset, uploaderSub string) (*imageclient.UploadResult, error)
	ReferencePing(ctx context.Context, hashes []string) (*imageclient.ReferencePingResult, error)
}

type runner struct {
	db         *gorm.DB
	cli        imageUploader
	source     Source
	mirror     *mirror
	gap        time.Duration
	stats      *Stats
	pingHashes []string
}

// labelResult is one label's outcome. fill RETURNS it rather than mutating the
// runner, which is what makes the worker pool safe: every field is private to
// one label, so results merge serially on the driver goroutine and nothing is
// shared while uploads are in flight. (The screenshot wave died at 8,077
// uploads to a `concurrent map read and map write` for want of this shape.)
type labelResult struct {
	missing, would, uploaded, raced, rejected, errors int
	quota                                             bool
	hash                                              string // fresh upload, for the reference ping
}

func (s *Stats) merge(r labelResult) {
	s.Missing += r.missing
	s.Would += r.would
	s.Uploaded += r.uploaded
	s.Raced += r.raced
	s.Rejected += r.rejected
	s.Errors += r.errors
	if r.quota {
		s.Quota = true
	}
}

// fill uploads one label's mirrored logo and writes logo_hash + provenance.
//
// The UPDATE re-asserts an empty logo_hash even though the candidate was selected
// on it. Between selection and here the other lane (or a human edit) may have
// given this label a logo, and precedence says the first writer keeps it — so
// losing that race must mean leaving the winner's logo alone, never overwriting
// it. RowsAffected == 0 is therefore an ordinary outcome (counted as raced),
// not an error. The bytes are already in the image service by then and are
// pinged regardless, so a raced upload costs a duplicate image, not a leak.
func (r *runner) fill(ctx context.Context, c candidate, apply bool) labelResult {
	var out labelResult
	path, ok := r.mirror.resolve(c.ExternalID)
	if !ok {
		out.missing++
		return out
	}
	if !apply {
		out.would++
		return out
	}

	res, err := r.upload(ctx, path)
	if err != nil {
		switch {
		case stderrors.Is(err, imageclient.ErrQuotaExceeded):
			slog.Warn("daily image quota exhausted — stopping", "label", c.LabelID)
			out.quota = true
		case stderrors.Is(err, imageclient.ErrModerationRejected):
			out.rejected++
			slog.Warn("logo rejected by moderation", "source", r.source.Key, "label", c.LabelID, "external_id", c.ExternalID)
		default:
			out.errors++
			slog.Warn("upload label logo", "source", r.source.Key, "label", c.LabelID, "external_id", c.ExternalID, "err", err)
		}
		return out
	}

	tx := r.db.WithContext(ctx).Exec(
		`UPDATE catalog_label SET logo_hash = ?, field_provenance = ?, updated_at = now()
		 WHERE id = ? AND logo_hash = '' AND deleted_at IS NULL`,
		res.Hash, mergeProvenance(c.FieldProvenance, r.source.Key, nowUTC()), c.LabelID)
	if tx.Error != nil {
		out.errors++
		slog.Warn("write label logo", "source", r.source.Key, "label", c.LabelID, "err", tx.Error)
		return out
	}
	// Ping whether or not the row was claimed: the bytes are in the image
	// service either way and sit at TTL from upload time.
	out.hash = res.Hash
	if tx.RowsAffected > 0 {
		out.uploaded++
	} else {
		out.raced++
	}
	return out
}

// mergeProvenance prepends this run's (source, at) entry to the logo_hash
// provenance array (latest first) inside the label's existing document, leaving
// every other field's provenance untouched. A malformed or empty document is
// treated as absent rather than fatal — the alternative is refusing to record
// provenance because previous provenance is unreadable.
func mergeProvenance(cur datatypes.JSON, source, at string) datatypes.JSON {
	doc := map[string]json.RawMessage{}
	if len(cur) > 0 {
		_ = json.Unmarshal(cur, &doc)
	}
	entry, _ := json.Marshal(provEntry{Source: source, At: at})
	var arr []json.RawMessage
	if raw, ok := doc[provField]; ok {
		_ = json.Unmarshal(raw, &arr)
	}
	arr = append([]json.RawMessage{entry}, arr...)
	merged, _ := json.Marshal(arr)
	doc[provField] = merged
	out, _ := json.Marshal(doc)
	return datatypes.JSON(out)
}

func nowUTC() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

// upload reads a mirrored logo and uploads it under the catalog_logo preset.
// Bytes only ever come from the local mirror — this never dials Bangumi or
// Ci-en. Transient failures are retried with a fresh bytes.Reader per attempt
// (the previous one is consumed) so the run survives an image-container
// recreation mid-sweep; quota and moderation are terminal.
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
		res, err := r.cli.UploadWithSub(ctx, bytes.NewReader(body), filename, logoPreset, r.source.UploaderSub)
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

// run walks the candidates through a fixed pool of workers. One label is one
// image and one row, so labels are independent; the only shared state is the
// result merge, which happens on the caller's goroutine, leaving Stats and
// pingHashes single-writer and needing no mutex.
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
			r.absorb(r.fill(ctx, c, opts.Apply))
		}
		return
	}

	// Quota exhaustion is terminal for the whole run, so it cancels the shared
	// context and every in-flight worker stops at its next label.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	in := make(chan candidate)
	out := make(chan labelResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range in {
				out <- r.fill(ctx, c, opts.Apply)
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
			slog.Info("label-logos progress", "source", r.source.Key, "done", done, "of", len(cands),
				"uploaded", r.stats.Uploaded, "errors", r.stats.Errors)
		}
	}
}

// absorb folds one label's result into the run-level state. Single-writer by
// construction — only the driver goroutine calls it.
func (r *runner) absorb(res labelResult) {
	r.stats.merge(res)
	if res.hash != "" {
		r.pingHashes = append(r.pingHashes, res.hash)
	}
}
