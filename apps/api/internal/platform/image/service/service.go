// Package service wires up the upload pipeline: auth-validated request →
// sniff → dedup → process → store → persist → response.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"api/internal/platform/image/model"
	"api/internal/platform/image/moderation"
	"api/internal/platform/image/preset"
	"api/internal/platform/image/processor"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/storage"

	"github.com/gabriel-vasile/mimetype"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Errors returned by the service layer. Handlers map these to error codes.
var (
	ErrUnsupportedFormat = errors.New("service: unsupported image format")
	ErrPresetNotFound    = errors.New("service: preset not defined")
	ErrPresetNotAllowed  = errors.New("service: preset not allowed for site")
	ErrMIMENotAllowed    = errors.New("service: mime not allowed by preset")
)

// UploadRequest is the internal representation of an upload. All auth-
// validated context (site, uploader) is passed in explicitly; the service
// layer does not reach into Fiber ctx.
type UploadRequest struct {
	Body           []byte
	Preset         string
	Site           string
	UploaderSub    string
	UploaderClient string
	UploaderIP     string
	// CDNBase is the calling client's per-site image CDN base ("" → global
	// default). Set from oauth_clients.image_cdn_base so the upload response
	// carries URLs under the client's own domain. See docs/image_service.
	CDNBase string
}

// UploadResult is what the handler turns into JSON.
type UploadResult struct {
	Hash        string            `json:"hash"`
	URL         string            `json:"url"`
	VariantURLs map[string]string `json:"variant_urls"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	// Thumbhash is the base64 ThumbHash placeholder so callers can persist it
	// alongside the hash (denormalized) and render a blur-up placeholder + the
	// correct aspect ratio without a second roundtrip. Empty for images whose
	// row predates the column until the backfill fills it.
	Thumbhash    string `json:"thumbhash,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
	Deduplicated bool   `json:"deduplicated"`
}

// Service is the upload orchestrator.
type Service struct {
	presets   *preset.Config
	storage   *storage.Client
	imgRepo   *repository.ImageRepository
	usageRepo *repository.SiteUsageRepository
	db        *gorm.DB // for enqueueing moderation queue entries
	mod       moderation.Provider
	syncTO    time.Duration
	cdnBase   string // e.g. https://cdn.example.com/img (no trailing slash)
}

// Options is the extended config for constructing a Service with
// moderation plumbing. All fields are optional; zero values give V1
// behavior (no moderation, no queue).
type Options struct {
	Moderation  moderation.Provider
	SyncTimeout time.Duration // default 300ms
	DB          *gorm.DB      // images DB, required to enqueue async work
}

// New builds a Service with its dependencies. Moderation and DB handle
// are optional (for V1 compatibility).
func New(
	presets *preset.Config,
	storage *storage.Client,
	imgRepo *repository.ImageRepository,
	usageRepo *repository.SiteUsageRepository,
	cdnBase string,
	opts ...Options,
) *Service {
	s := &Service{
		presets:   presets,
		storage:   storage,
		imgRepo:   imgRepo,
		usageRepo: usageRepo,
		cdnBase:   strings.TrimRight(cdnBase, "/"),
		mod:       moderation.NewNoop(),
		syncTO:    300 * time.Millisecond,
	}
	if len(opts) > 0 {
		o := opts[0]
		if o.Moderation != nil {
			s.mod = o.Moderation
		}
		if o.SyncTimeout > 0 {
			s.syncTO = o.SyncTimeout
		}
		if o.DB != nil {
			s.db = o.DB
		}
	}
	return s
}

// ErrModerationRejected is returned by Upload when the sync moderation
// step outright rejects the content.
var ErrModerationRejected = errors.New("service: rejected by moderation")

// Presets exposes the loaded preset config (handlers may need it for
// MIME / allow checks without duplicating lookup logic).
func (s *Service) Presets() *preset.Config { return s.presets }

// Upload runs the full upload pipeline. It is safe to call concurrently:
// two racing uploads of the same hash may both run full libvips work but
// the final DB INSERT deduplicates (one wins, other's work is discarded).
func (s *Service) Upload(ctx context.Context, req UploadRequest) (*UploadResult, error) {
	ps, ok := s.presets.Get(req.Preset)
	if !ok {
		return nil, ErrPresetNotFound
	}

	// MIME sniff on the raw bytes. Never trust Content-Type from client.
	mt := mimetype.Detect(req.Body).String()
	if !ps.IsMIMEAllowed(mt) {
		return nil, fmt.Errorf("%w: %s", ErrMIMENotAllowed, mt)
	}

	// Content-addressed hash (computed once, used everywhere).
	sum := sha256.Sum256(req.Body)
	hash := hex.EncodeToString(sum[:])

	// Fast path: hash already known. Just ensure preset's variants exist
	// and return. (No re-moderation — the hash's verdict is final.)
	existing, err := s.imgRepo.FindByHash(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("lookup hash: %w", err)
	}
	if existing != nil {
		result, err := s.handleExisting(ctx, existing, ps, req)
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	// A soft-deleted row still occupies UNIQUE(hash). Revive it instead of
	// INSERTing a duplicate (which would violate the unique index → 500).
	// Its S3 objects are guaranteed present while the row exists (GC removes
	// objects + row together), and the hash's prior moderation verdict is
	// final, so re-route through the dedup path without re-moderating.
	if deleted, err := s.imgRepo.FindByHashIncludingDeleted(ctx, hash); err != nil {
		return nil, fmt.Errorf("lookup hash (incl deleted): %w", err)
	} else if deleted != nil {
		if err := s.imgRepo.Resurrect(ctx, hash); err != nil {
			return nil, fmt.Errorf("resurrect hash: %w", err)
		}
		deleted.DeletedAt = nil
		return s.handleExisting(ctx, deleted, ps, req)
	}

	// New hash — run sync moderation first. Hard rejections short-circuit
	// before any expensive processing.
	syncCtx, cancel := context.WithTimeout(ctx, s.syncTO)
	defer cancel()
	syncDecision, syncErr := s.mod.SyncCheck(syncCtx, req.Body, mt)
	if syncErr != nil {
		slog.Warn("moderation sync errored; treating as undecided",
			"provider", s.mod.Name(), "err", syncErr)
	}
	if syncDecision != nil && syncDecision.Verdict == moderation.VerdictReject {
		return nil, fmt.Errorf("%w: %s", ErrModerationRejected, syncDecision.Reason)
	}

	return s.handleNew(ctx, hash, mt, ps, req, syncDecision)
}

// handleExisting handles the dedup-hit case. Checks which variants are
// missing for this preset, generates them, uploads, updates DB.
func (s *Service) handleExisting(ctx context.Context, img *model.Image, ps preset.Preset, req UploadRequest) (*UploadResult, error) {
	haveVariants := img.VariantList()
	var missing []preset.VariantSpec
	for _, v := range ps.Variants {
		if !slices.Contains(haveVariants, v.Name) {
			missing = append(missing, v)
		}
	}

	// If no variants missing, still record site-usage + refresh TTL.
	if len(missing) > 0 {
		// Need to fetch main image bytes to regenerate derivatives from.
		rc, err := s.storage.Get(ctx, img.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("fetch main for variant backfill: %w", err)
		}
		mainBytes, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read main: %w", err)
		}
		srcImg, _, err := processor.DecodeFromBytes(mainBytes)
		if err != nil {
			return nil, fmt.Errorf("decode main for backfill: %w", err)
		}
		for _, v := range missing {
			out, err := processor.ProcessVariant(srcImg, v)
			if err != nil {
				return nil, fmt.Errorf("variant %s: %w", v.Name, err)
			}
			variantKey := variantStorageKey(img.Hash, out.VariantName, out.Ext)
			if err := s.storage.Put(ctx, variantKey, out.Data, out.MIME); err != nil {
				return nil, fmt.Errorf("store variant %s: %w", v.Name, err)
			}
			haveVariants = append(haveVariants, v.Name)
		}
		if err := s.imgRepo.UpdateVariants(ctx, img.Hash, haveVariants); err != nil {
			return nil, fmt.Errorf("persist variants: %w", err)
		}
	}

	// Touch last_referenced_at + record site usage.
	if _, err := s.imgRepo.TouchReferenced(ctx, []string{img.Hash}); err != nil {
		return nil, fmt.Errorf("touch: %w", err)
	}
	if err := s.usageRepo.RecordUpload(ctx, img.Hash, req.Site, req.UploaderSub, req.UploaderClient); err != nil {
		return nil, fmt.Errorf("record usage: %w", err)
	}

	return s.buildResult(req.CDNBase, img.Hash, img.Ext, img.Thumbhash, img.Width, img.Height, img.SizeBytes, ps, true), nil
}

// handleNew runs the full pipeline for an unseen hash.
func (s *Service) handleNew(ctx context.Context, hash, originMIME string, ps preset.Preset, req UploadRequest, syncDec *moderation.Decision) (*UploadResult, error) {
	srcImg, _, err := processor.DecodeFromBytes(req.Body)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	mainOut, err := processor.ProcessMain(srcImg, s.presets.MainPipeline)
	if err != nil {
		return nil, fmt.Errorf("process main: %w", err)
	}

	mainKey := mainStorageKey(hash, mainOut.Ext)
	if err := s.storage.Put(ctx, mainKey, mainOut.Data, mainOut.MIME); err != nil {
		return nil, fmt.Errorf("store main: %w", err)
	}

	variantNames := make([]string, 0, len(ps.Variants))
	for _, v := range ps.Variants {
		out, err := processor.ProcessVariant(srcImg, v)
		if err != nil {
			return nil, fmt.Errorf("variant %s: %w", v.Name, err)
		}
		key := variantStorageKey(hash, out.VariantName, out.Ext)
		if err := s.storage.Put(ctx, key, out.Data, out.MIME); err != nil {
			return nil, fmt.Errorf("store variant %s: %w", v.Name, err)
		}
		variantNames = append(variantNames, v.Name)
	}

	// Seed review_status from sync decision. If undecided (e.g. sync
	// provider timed out or isn't configured) and we have an async queue,
	// leave the image in "pending" and enqueue; otherwise approve by
	// default (V1 behavior preserved).
	reviewStatus := model.ReviewApproved
	enqueueAsync := false
	var reviewLabels datatypes.JSON
	if syncDec != nil {
		switch syncDec.Verdict {
		case moderation.VerdictApprove:
			reviewStatus = model.ReviewApproved
		case moderation.VerdictReview:
			reviewStatus = model.ReviewManual
		case moderation.VerdictUndecided:
			if s.db != nil && s.mod.Name() != "noop" {
				reviewStatus = model.ReviewPending
				enqueueAsync = true
			}
		}
		if syncDec.Labels != nil {
			if raw, err := json.Marshal(syncDec.Labels); err == nil {
				reviewLabels = datatypes.JSON(raw)
			}
		}
	}

	now := time.Now()
	img := &model.Image{
		Hash:                hash,
		StorageKey:          mainKey,
		MIME:                mainOut.MIME,
		Ext:                 mainOut.Ext,
		Width:               mainOut.Width,
		Height:              mainOut.Height,
		Thumbhash:           mainOut.Thumbhash,
		SizeBytes:           int64(len(mainOut.Data)),
		OriginMIME:          originMIME,
		OriginSize:          int64(len(req.Body)),
		ReviewStatus:        reviewStatus,
		ReviewLabels:        reviewLabels,
		FirstUploaderSub:    req.UploaderSub,
		FirstUploaderClient: req.UploaderClient,
		FirstUploaderIP:     req.UploaderIP,
	}
	if reviewStatus != model.ReviewPending {
		img.ReviewedAt = &now
	}
	img.SetVariants(variantNames)
	inserted, err := s.imgRepo.Create(ctx, img)
	if err != nil {
		return nil, fmt.Errorf("persist image: %w", err)
	}
	if !inserted {
		// Lost a race with a concurrent first-upload of the same hash (the
		// OnConflict turned our INSERT into a no-op). Converge on the winning
		// row via the dedup path so this request still records site usage and
		// backfills any preset variants it needs — honoring the documented
		// concurrency guarantee instead of 500ing on the unique index.
		winner, err := s.imgRepo.FindByHash(ctx, hash)
		if err != nil {
			return nil, fmt.Errorf("lookup after conflict: %w", err)
		}
		if winner == nil {
			return nil, fmt.Errorf("persist image: conflict but row not found")
		}
		return s.handleExisting(ctx, winner, ps, req)
	}

	if err := s.usageRepo.RecordUpload(ctx, hash, req.Site, req.UploaderSub, req.UploaderClient); err != nil {
		return nil, fmt.Errorf("record usage: %w", err)
	}

	if enqueueAsync {
		qe := &model.ModerationQueue{Hash: hash, Site: req.Site}
		if err := s.db.WithContext(ctx).Create(qe).Error; err != nil {
			// Don't fail the upload; just log — worker can't pick it up
			// now, but next upload of this hash will still have it pending.
			slog.Warn("enqueue moderation failed", "hash", hash, "err", err)
		}
	}

	return s.buildResult(req.CDNBase, hash, mainOut.Ext, mainOut.Thumbhash, mainOut.Width, mainOut.Height, int64(len(mainOut.Data)), ps, false), nil
}

// SoftDelete marks an image deleted (sets deleted_at) when the calling
// `site` has a usage record for `hash`. The GC worker physically removes the
// S3 objects + row after the hard-delete TTL. It is deliberately:
//   - soft (recoverable until GC) — safe under content dedup, where a hash
//     may be shared and a hard delete would break other referrers;
//   - site-scoped — a client can only retire images its own site referenced.
//
// Returns false (not found) when the caller's site never used the hash, so
// one client can't retire another's images. For irreversible compliance
// deletes use the admin force path.
func (s *Service) SoftDelete(ctx context.Context, hash, site string) (bool, error) {
	if s.db == nil {
		// Misconfigured wiring (service built without Options{DB}). Return a
		// clear error instead of a nil-pointer panic.
		return false, errors.New("service: SoftDelete requires a DB handle")
	}
	var usage int64
	if err := s.db.WithContext(ctx).
		Model(&model.ImageSiteUsage{}).
		Where("hash = ? AND site = ?", hash, site).
		Count(&usage).Error; err != nil {
		return false, err
	}
	if usage == 0 {
		return false, nil
	}
	if err := s.db.WithContext(ctx).
		Model(&model.Image{}).
		Where("hash = ?", hash).
		Update("deleted_at", time.Now()).Error; err != nil {
		return false, err
	}
	return true, nil
}

// buildResult composes the JSON-shaped result including variant URLs.
// clientBase is the calling client's image CDN base ("" → global default) so
// each site's upload response carries URLs under its own domain.
func (s *Service) buildResult(clientBase, hash, ext, thumbhash string, w, h int, size int64, ps preset.Preset, dedup bool) *UploadResult {
	base := s.resolveCDNBase(clientBase)
	variants := make(map[string]string, len(ps.Variants))
	for _, v := range ps.Variants {
		variants[v.Name] = joinURL(base, variantStorageKey(hash, v.Name, "webp"))
	}
	return &UploadResult{
		Hash:         hash,
		URL:          joinURL(base, mainStorageKey(hash, ext)),
		VariantURLs:  variants,
		Width:        w,
		Height:       h,
		Thumbhash:    thumbhash,
		SizeBytes:    size,
		Deduplicated: dedup,
	}
}

// resolveCDNBase returns the per-client CDN base when set, else the global
// default. Empty/whitespace clientBase falls back so existing clients (which
// don't set one) keep serving under the global domain unchanged.
func (s *Service) resolveCDNBase(clientBase string) string {
	if b := strings.TrimRight(clientBase, "/"); b != "" {
		return b
	}
	return s.cdnBase
}

// MainURL builds a hash's main-image URL under the GLOBAL CDN base. Used by
// callers without a per-client context (admin tooling, avatar URLs).
func (s *Service) MainURL(hash, ext string) string {
	return s.MainURLFor("", hash, ext)
}

// VariantURL builds a variant URL under the GLOBAL CDN base.
func (s *Service) VariantURL(hash, variant, ext string) string {
	return s.VariantURLFor("", hash, variant, ext)
}

// MainURLFor builds a hash's main-image URL under the calling client's CDN
// base (clientBase=="" → global default).
func (s *Service) MainURLFor(clientBase, hash, ext string) string {
	return joinURL(s.resolveCDNBase(clientBase), mainStorageKey(hash, ext))
}

// VariantURLFor builds a variant URL under the calling client's CDN base.
func (s *Service) VariantURLFor(clientBase, hash, variant, ext string) string {
	return joinURL(s.resolveCDNBase(clientBase), variantStorageKey(hash, variant, ext))
}

// joinURL joins a resolved CDN base with a storage key.
func joinURL(base, key string) string {
	return fmt.Sprintf("%s/%s", base, key)
}

// mainStorageKey returns the S3 key for a hash's main image.
// Key format: <hash[:2]>/<hash[2:4]>/<hash>.<ext>
func mainStorageKey(hash, ext string) string {
	return fmt.Sprintf("%s/%s/%s.%s", hash[:2], hash[2:4], hash, ext)
}

// variantStorageKey returns the S3 key for a hash's variant.
// Key format: <hash[:2]>/<hash[2:4]>/<hash>_<variant>.<ext>
func variantStorageKey(hash, variant, ext string) string {
	return fmt.Sprintf("%s/%s/%s_%s.%s", hash[:2], hash[2:4], hash, variant, ext)
}

// GetByHash fetches the full image record + its sites.
func (s *Service) GetByHash(ctx context.Context, hash string) (*model.Image, []string, error) {
	img, err := s.imgRepo.FindByHash(ctx, hash)
	if err != nil || img == nil {
		return nil, nil, err
	}
	sites, err := s.usageRepo.SitesForHash(ctx, hash)
	if err != nil {
		return img, nil, err
	}
	return img, sites, nil
}

// MetaBatch returns intrinsic metadata (width/height/thumbhash) for the
// subset of the given hashes that exist, keyed by hash. Powers the batch
// meta endpoint so consumers can render placeholders + reserve aspect ratio
// for many images in one roundtrip. Metadata is immutable per hash.
func (s *Service) MetaBatch(ctx context.Context, hashes []string) (map[string]repository.ImageMeta, error) {
	return s.imgRepo.MetaByHashes(ctx, hashes)
}

// ReferencePing refreshes last_referenced_at for the given hashes filtered
// to those the caller's site has actually used. Returns (updated, notFound).
func (s *Service) ReferencePing(ctx context.Context, site string, hashes []string) (int64, []string, error) {
	if len(hashes) == 0 {
		return 0, nil, nil
	}
	// Site-scoped: only hashes the caller's own site has used (and that still
	// exist) can be kept alive. A client cannot ref-ping arbitrary global
	// hashes it never referenced, despite the old global behavior.
	existing, err := s.usageRepo.ExistingHashesForSite(ctx, site, hashes)
	if err != nil {
		return 0, nil, err
	}
	existingSet := make(map[string]struct{}, len(existing))
	for _, h := range existing {
		existingSet[h] = struct{}{}
	}
	notFound := make([]string, 0)
	for _, h := range hashes {
		if _, ok := existingSet[h]; !ok {
			notFound = append(notFound, h)
		}
	}
	updated, err := s.imgRepo.TouchReferenced(ctx, existing)
	return updated, notFound, err
}

// Kept in case we need to escape hash for URL in future;
// content-addressed hex hashes are always safe but explicit is safer.
var _ = url.PathEscape
