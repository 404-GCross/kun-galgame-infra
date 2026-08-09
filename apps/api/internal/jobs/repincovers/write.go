package repincovers

import (
	"bytes"
	"context"
	"encoding/csv"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	// coverPreset is the image-service preset catalog covers upload under.
	coverPreset = "catalog_cover"
	// uploaderSub stamps a machine identity on first_uploader_sub. There is no
	// human behind a re-pin wave and the audit trail should say so.
	uploaderSub = "system:repin-portrait-covers"
	// upscaleSourceKey is the catalog_source a super-resolution product is
	// filed under. The product is a DERIVED image, never the source's own art.
	upscaleSourceKey = "upscale"
	// uploadRetries rides out an image-container recreation mid-run. Quota and
	// moderation are terminal and never retried.
	uploadRetries = 6
	// downloadTimeout bounds one CDN fetch during --export-dir.
	downloadTimeout = 60 * time.Second
)

// badUpscaleKinds are the inherited kinds that prove a super-resolution
// product enlarges something that is not a cover at all.
var badUpscaleKinds = []string{"pkgback", "pkgmed", "pkgcontent", "pkgside"}

// url renders a hash as its complete CDN URL.
func (r *runner) url(hash string) string { return r.cli.MainURL(hash) }

// writePlanCSV dumps the whole plan for human review. The URLs are what make
// it reviewable: the point of this wave is that a machine picked the wrong
// PICTURE, and only an eye can confirm the new one is right.
func writePlanCSV(path string, plans []Plan, url func(string) string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	head := []string{"work_id", "action", "old_kind", "old_source", "old_edge", "old_url",
		"new_kind", "new_source", "new_edge", "new_url"}
	if err := w.Write(head); err != nil {
		return err
	}
	for _, p := range plans {
		if p.Action == ActionNone {
			continue
		}
		rec := []string{strconv.FormatInt(p.WorkID, 10), p.Action.String()}
		if p.Old != nil {
			rec = append(rec, p.Old.Kind, p.Old.SourceKey, strconv.Itoa(p.Old.LongEdge()), url(p.Old.Hash))
		} else {
			rec = append(rec, "", "", "", "")
		}
		rec = append(rec, p.New.Kind, p.New.SourceKey, strconv.Itoa(p.New.LongEdge()), url(p.New.Hash))
		if err := w.Write(rec); err != nil {
			return err
		}
	}
	return nil
}

// productName is the filename a winner travels under, both out to the upscaler
// and back. It carries the work id AND the origin hash because the product is
// a new image with a new hash: without the pair in the name there is nothing
// left to tell reinject which work the file belongs to. upscale-bench keeps the
// relative path and only swaps the suffix, so the name survives the round trip.
func productName(p Plan) string {
	return fmt.Sprintf("%d__%s.webp", p.WorkID, p.New.Hash)
}

// parseProductName reads back what productName wrote.
func parseProductName(name string) (workID int64, originHash string, ok bool) {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	id, hash, found := strings.Cut(base, "__")
	if !found {
		return 0, "", false
	}
	workID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || hash == "" {
		return 0, "", false
	}
	return workID, hash, true
}

// export downloads the under-target winners so upscale-bench can enlarge them.
// Read-only against both the database and image_service.
func (r *runner) export(ctx context.Context, opts Opts, plans []Plan) error {
	if err := os.MkdirAll(opts.ExportDir, 0o755); err != nil {
		return err
	}
	todo := actionable(plans, ActionUpscale, opts.Limit)
	client := &http.Client{Timeout: downloadTimeout}
	for _, p := range todo {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dst := filepath.Join(opts.ExportDir, productName(p))
		if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
			continue // resumable: already exported
		}
		if err := download(ctx, client, r.url(p.New.Hash), dst); err != nil {
			r.stats.Errors++
			slog.Warn("export cover", "work", p.WorkID, "hash", p.New.Hash, "err", err)
			continue
		}
		r.stats.Exported++
	}
	slog.Info("export complete", "dir", opts.ExportDir, "files", r.stats.Exported, "errors", r.stats.Errors)
	return nil
}

func download(ctx context.Context, client *http.Client, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("GET %s: empty body", url)
	}
	// Write through a temp file so an interrupted run cannot leave a truncated
	// image that the resume check would then accept as done.
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// reinject uploads the upscaled products, writes one cover row each and moves
// the pin onto it. The origin row is never touched: it keeps its place in the
// gallery, so a rollback is a flag flip and the bytes never lose their
// reference.
func (r *runner) reinject(ctx context.Context, opts Opts, plans []Plan) error {
	if !opts.Apply {
		slog.Warn("reinject is a DRY listing without --apply")
	}
	srcID, err := r.sourceID(ctx, upscaleSourceKey)
	if err != nil {
		return err
	}
	byWork := map[int64]Plan{}
	for _, p := range plans {
		if p.Action == ActionUpscale {
			byWork[p.WorkID] = p
		}
	}
	entries, err := os.ReadDir(opts.ReinjectDir)
	if err != nil {
		return fmt.Errorf("read reinject dir: %w", err)
	}
	acted := 0
	for _, e := range entries {
		if ctx.Err() != nil || r.stats.QuotaBreak {
			break
		}
		if e.IsDir() {
			continue
		}
		workID, originHash, ok := parseProductName(e.Name())
		if !ok {
			continue
		}
		p, planned := byWork[workID]
		if !planned || p.New.Hash != originHash {
			// The plan moved on since the export (someone re-pinned, or a
			// better cover arrived). Acting anyway would pin an enlargement of
			// a picture the ladder no longer chooses.
			r.stats.Skipped++
			continue
		}
		if opts.Limit > 0 && acted >= opts.Limit {
			r.stats.Skipped++
			continue
		}
		if !opts.Apply {
			acted++
			continue
		}
		if err := r.injectOne(ctx, filepath.Join(opts.ReinjectDir, e.Name()), p, srcID); err != nil {
			r.stats.Errors++
			slog.Warn("reinject cover", "work", workID, "err", err)
			continue
		}
		acted++
	}
	return r.finish(ctx)
}

// injectOne uploads one product and pins the row it creates.
func (r *runner) injectOne(ctx context.Context, path string, p Plan, srcID int16) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("empty product file")
	}
	res, err := r.upload(ctx, body, filepath.Base(path))
	if err != nil {
		return err
	}
	r.stats.Uploaded++
	r.fresh = append(r.fresh, res.Hash)

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var next int
		if err := tx.Raw(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM catalog_work_cover WHERE work_id = ?`,
			p.WorkID).Scan(&next).Error; err != nil {
			return err
		}
		row := model.CatalogWorkCover{
			WorkID: p.WorkID, ImageHash: res.Hash, SortOrder: next,
			// The product IS the winner, enlarged: it depicts the same picture,
			// so it inherits the kind and the content flags rather than being
			// filed as an unknown. That inheritance is also the only provenance
			// a product carries (no origin-hash column exists), and it is what
			// made this wave's 47 bad enlargements findable at all.
			Kind: p.New.Kind, PortraitPinned: false,
			Sexual: p.New.Sexual, Violence: 0, SourceID: srcID,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return r.repin(tx, p.WorkID, row.ID)
	})
}

// directPin moves the pin for winners that are already display-ready.
func (r *runner) directPin(ctx context.Context, opts Opts, plans []Plan) error {
	todo := actionable(plans, ActionDirectPin, opts.Limit)
	for _, p := range todo {
		if ctx.Err() != nil {
			break
		}
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return r.repin(tx, p.WorkID, p.New.ID)
		})
		if err != nil {
			r.stats.Errors++
			slog.Warn("direct pin", "work", p.WorkID, "err", err)
		}
	}
	return r.finish(ctx)
}

// repin makes coverID the work's ONE pinned cover. Clearing and setting in the
// same transaction is what keeps "at most one pin per work" true even if the
// process dies mid-run.
func (r *runner) repin(tx *gorm.DB, workID, coverID int64) error {
	if err := tx.Exec(`UPDATE catalog_work_cover SET portrait_pinned = false, updated_at = now()
		WHERE work_id = ? AND portrait_pinned AND id <> ?`, workID, coverID).Error; err != nil {
		return err
	}
	res := tx.Exec(`UPDATE catalog_work_cover SET portrait_pinned = true, updated_at = now()
		WHERE id = ? AND work_id = ?`, coverID, workID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("cover %d is not on work %d", coverID, workID)
	}
	r.stats.Repinned++
	r.touched = append(r.touched, workID)
	return nil
}

// purge deletes the super-resolution rows whose inherited kind proves they
// enlarge a box back, a disc face, a booklet page or a spine.
//
// This is the wave's ONLY destructive step, so it is its own mode and lists
// every row before touching anything. Bytes are not deleted here: several of
// these hashes are shared by a second work's row (re-released titles share a
// scan), and reference counting belongs to the image service's own GC, which
// collects a hash once the last row referencing it is gone.
func (r *runner) purge(ctx context.Context, opts Opts) error {
	var rows []coverRow
	if err := r.db.WithContext(ctx).Raw(`
		SELECT c.id, c.work_id, c.image_hash, c.kind, s.key AS source_key,
		       c.sexual, c.sort_order, c.portrait_pinned
		FROM catalog_work_cover c
		JOIN catalog_source s ON s.id = c.source_id
		WHERE s.key = ? AND c.kind IN ?
		ORDER BY c.work_id`, upscaleSourceKey, badUpscaleKinds).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load bad upscales: %w", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		slog.Info("bad upscale", "cover_id", row.ID, "work", row.WorkID, "kind", row.Kind,
			"pinned", row.Pinned, "url", r.urlFor(row.Hash))
		ids = append(ids, row.ID)
	}
	if len(ids) == 0 || !opts.Apply {
		return nil
	}
	// A pinned row must lose its pin before it is deleted, or the work would
	// simply have no portrait pin afterwards. Every row here is a bad pick, so
	// the correct successor comes from the re-pin pass — which is why purge is
	// meant to run AFTER it, and why a still-pinned row at this point is a
	// refusal rather than something to paper over.
	var stillPinned int64
	if err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM catalog_work_cover WHERE id IN ? AND portrait_pinned`, ids).Scan(&stillPinned).Error; err != nil {
		return err
	}
	if stillPinned > 0 {
		return fmt.Errorf("%d of the %d bad upscale rows are still pinned; run the re-pin pass first", stillPinned, len(ids))
	}
	res := r.db.WithContext(ctx).Exec(`DELETE FROM catalog_work_cover WHERE id IN ?`, ids)
	if res.Error != nil {
		return res.Error
	}
	r.stats.Purged = int(res.RowsAffected)
	return nil
}

// urlFor renders a CDN URL when a client is wired, and the bare hash when it
// is not (the purge mode does not need image_service).
func (r *runner) urlFor(hash string) string {
	if r.cli == nil {
		return hash
	}
	return r.cli.MainURL(hash)
}

// isQuota / isRejected split the two terminal upload verdicts from the
// transient ones: an exhausted daily quota stops the whole run, a moderation
// refusal is one image's verdict and the next file still gets its turn.
func isQuota(err error) bool    { return stderrors.Is(err, imageclient.ErrQuotaExceeded) }
func isRejected(err error) bool { return stderrors.Is(err, imageclient.ErrModerationRejected) }

// upload pushes one product under the catalog_cover preset.
func (r *runner) upload(ctx context.Context, body []byte, filename string) (*imageclient.UploadResult, error) {
	var lastErr error
	for attempt := 0; attempt < uploadRetries; attempt++ {
		if r.gap > 0 {
			time.Sleep(r.gap)
		}
		res, err := r.cli.UploadWithSub(ctx, bytes.NewReader(body), filename, coverPreset, uploaderSub)
		if err == nil {
			return res, nil
		}
		if isQuota(err) {
			r.stats.QuotaBreak = true
			return nil, err
		}
		if isRejected(err) {
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

// finish moves the changes-feed watermark for the works that actually changed
// and keeps freshly uploaded bytes alive. An image sits at TTL from upload
// time, so waiting for the nightly refping risks uploading bytes and losing
// them.
func (r *runner) finish(ctx context.Context) error {
	if err := repository.TouchWorks(ctx, r.db, r.touched); err != nil {
		return fmt.Errorf("touch works: %w", err)
	}
	if r.cli == nil || len(r.fresh) == 0 {
		return nil
	}
	for i := 0; i < len(r.fresh); i += 1000 {
		batch := r.fresh[i:min(i+1000, len(r.fresh))]
		if _, err := r.cli.ReferencePing(ctx, batch); err != nil {
			slog.Warn("refping fresh hashes", "err", err)
			return nil
		}
	}
	return nil
}

// sourceID resolves a catalog_source key, refusing an unseeded registry rather
// than writing a row with a source of 0.
func (r *runner) sourceID(ctx context.Context, key string) (int16, error) {
	var id int16
	if err := r.db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("catalog_source %q is not seeded", key)
	}
	return id, nil
}
