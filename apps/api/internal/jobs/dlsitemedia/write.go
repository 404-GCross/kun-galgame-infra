package dlsitemedia

import (
	"bytes"
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"time"

	"api/internal/platform/catalog/model"
	"api/pkg/imageclient"

	"gorm.io/gorm/clause"
)

const (
	// langJa is the BCP-47 language of a DLsite description.
	//
	// It is the SHORT code "ja", NOT "ja_jp": the claimed-side galgame intro
	// bridge pivots its four fixed columns to BCP-47 short codes
	// (galgameIntroPivot: intro_ja_jp→"ja", intro_en_us→"en", intro_zh_cn→
	// "zh-Hans", intro_zh_tw→"zh-Hant"), and the step-52 VNDB intro pilot writes
	// "en". Bodyless and claimed MUST share one lang vocabulary or the consumer's
	// per-language selection mis-fires (§3). The step-55 spec's "ja_jp" note was
	// pre-verification; the code's actual vocabulary is the short code, so this
	// wave writes "ja".
	langJa = "ja"

	// coverPreset / shotPreset are the image-service presets these uploads use
	// (configs/image_presets.yaml). The catalog image client's
	// image_allowed_presets MUST contain both, else every upload is 403'd.
	coverPreset = "catalog_cover"
	shotPreset  = "catalog_screenshot"

	// uploaderSub stamps a machine identity onto first_uploader_sub so the
	// backfilled image rows are traceable (there is no human uploader).
	uploaderSub = "system:dlsite-media-backfill"
)

// writeIntro writes one catalog_work_intro row for a bodyless work from the
// DLsite description. Pure DB — no bytes, no image service, no quota. Idempotent:
// a preloaded existing row (or an ON CONFLICT hit) is a skip.
func (r *runner) writeIntro(ctx context.Context, c candidate, m dlsiteMeta, apply bool) {
	if !isBodyless(c.Site) { // XOR guard (§8.D) — never materialise a claimed work
		r.c.introRefused++
		return
	}
	if m.Intro == "" {
		r.c.introNoText++
		return
	}
	if r.exist.intro[c.WorkID] {
		r.c.introExists++
		return
	}
	if !apply {
		r.c.introWould++
		return
	}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "lang"}, {Name: "source_id"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkIntro{
		WorkID: c.WorkID, Lang: langJa, Intro: m.Intro, SourceID: r.sourceID,
	})
	if res.Error != nil {
		r.c.errors++
		slog.Warn("write intro", "work", c.WorkID, "workno", c.Workno, "err", res.Error)
		return
	}
	if res.RowsAffected == 0 { // concurrent writer / already present
		r.c.introExists++
		return
	}
	r.exist.intro[c.WorkID] = true
	r.c.introWritten++
}

// writeCover uploads a bodyless work's landscape store cover (image_main) from
// the mirror and writes one catalog_work_cover row. Returns quota=true when the
// daily image quota is exhausted (caller aborts). portrait_pinned is always
// false — DLsite store covers are landscape; bodyless works have no portrait
// cover (only claimed works bridge a VNDB portrait), a data fact (§data-reality).
func (r *runner) writeCover(ctx context.Context, dir string, c candidate, m dlsiteMeta, apply bool) (quota bool) {
	if !isBodyless(c.Site) {
		r.c.coverRefused++
		return false
	}
	if m.CoverFile == "" { // placeholder (no_img_main) or absent
		r.c.coverPlaceholder++
		return false
	}
	if r.exist.cover[c.WorkID] {
		r.c.coverExists++
		return false
	}
	path := mirrorPath(dir, c.Workno, m.CoverFile)
	if !fileExists(path) {
		r.c.coverMissing++
		return false
	}
	if !apply {
		r.c.coverWould++
		return false
	}
	res, upErr := r.upload(ctx, path, m.CoverFile, coverPreset)
	if upErr != nil {
		return r.classifyUpload(upErr, "cover", c, &r.c.coverRejected)
	}
	tx := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "work_id"}, {Name: "image_hash"}},
		DoNothing: true,
	}).Create(&model.CatalogWorkCover{
		WorkID: c.WorkID, ImageHash: res.Hash, SortOrder: 0, Kind: "main",
		PortraitPinned: false, Sexual: ageToSexual(m.Age), Violence: 0, SourceID: r.sourceID,
	})
	if tx.Error != nil {
		r.c.errors++
		slog.Warn("write cover row", "work", c.WorkID, "err", tx.Error)
		return false
	}
	r.pingHashes = append(r.pingHashes, res.Hash) // keep the byte alive immediately
	if tx.RowsAffected == 0 {
		r.c.coverDedup++
		return false
	}
	r.exist.cover[c.WorkID] = true
	r.c.coverUploaded++
	return false
}

// writeScreenshots uploads a bodyless work's sample images (image_samples[]) from
// the mirror and writes one catalog_work_screenshot row each, sort_order = the
// sample's index. Per-sample idempotent (skip an index already present), so an
// interrupted run resumes cleanly. Returns quota=true on quota exhaustion.
func (r *runner) writeScreenshots(ctx context.Context, dir string, c candidate, m dlsiteMeta, apply bool) (quota bool) {
	if !isBodyless(c.Site) {
		r.c.shotRefused++
		return false
	}
	present := r.exist.shot[c.WorkID]
	for i, fname := range m.SampleFiles {
		if present != nil && present[i] {
			r.c.shotExists++
			continue
		}
		path := mirrorPath(dir, c.Workno, fname)
		if !fileExists(path) {
			r.c.shotMissing++
			continue
		}
		if !apply {
			r.c.shotWould++
			continue
		}
		res, upErr := r.upload(ctx, path, fname, shotPreset)
		if upErr != nil {
			if r.classifyUpload(upErr, "screenshot", c, &r.c.shotRejected) {
				return true
			}
			continue
		}
		tx := r.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "work_id"}, {Name: "image_hash"}},
			DoNothing: true,
		}).Create(&model.CatalogWorkScreenshot{
			WorkID: c.WorkID, ImageHash: res.Hash, SortOrder: i, Caption: "",
			Sexual: ageToSexual(m.Age), Violence: 0, SourceID: r.sourceID,
		})
		if tx.Error != nil {
			r.c.errors++
			slog.Warn("write screenshot row", "work", c.WorkID, "sort", i, "err", tx.Error)
			continue
		}
		r.pingHashes = append(r.pingHashes, res.Hash)
		if tx.RowsAffected == 0 {
			r.c.shotDedup++
			continue
		}
		if present == nil {
			present = map[int]bool{}
			r.exist.shot[c.WorkID] = present
		}
		present[i] = true
		r.c.shotUploaded++
	}
	return false
}

// upload reads a mirrored image and uploads it under the given preset. The bytes
// only ever come from the local mirror — this never dials DLsite.
func (r *runner) upload(ctx context.Context, path, filename, preset string) (*imageclient.UploadResult, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, os.ErrNotExist
	}
	if r.gap > 0 {
		time.Sleep(r.gap)
	}
	return r.cli.UploadWithSub(ctx, bytes.NewReader(body), filename, preset, uploaderSub)
}

// classifyUpload maps an upload error to a counter. Returns quota=true only for
// ErrQuotaExceeded (the caller aborts the whole run); moderation rejection is
// counted and the run continues; any other error (incl. a vanished file) counts
// as a generic error.
func (r *runner) classifyUpload(err error, kind string, c candidate, rejected *int) (quota bool) {
	switch {
	case stderrors.Is(err, imageclient.ErrQuotaExceeded):
		return true
	case stderrors.Is(err, imageclient.ErrModerationRejected):
		*rejected++
		slog.Warn(kind+" rejected by moderation", "work", c.WorkID, "workno", c.Workno)
		return false
	default:
		r.c.errors++
		slog.Warn("upload "+kind, "work", c.WorkID, "workno", c.Workno, "err", err)
		return false
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
