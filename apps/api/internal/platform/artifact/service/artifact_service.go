// Package service holds the artifact business logic: presigned two-tier upload
// (single PUT / multipart), HeadObject size verification on completion, and
// presigned/Worker download. See docs/artifact/.
package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"api/internal/platform/artifact/dto"
	"api/internal/platform/artifact/model"
	"api/internal/platform/artifact/quota"
	"api/internal/platform/artifact/repository"
	"api/internal/platform/artifact/storage"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Service-level sentinel errors; handlers map these to response codes.
var (
	ErrTooBig       = stderrors.New("artifact: file exceeds per-site max size")
	ErrMIMEDenied   = stderrors.New("artifact: file type not allowed for this site")
	ErrQuotaCount   = stderrors.New("artifact: daily file-count quota exceeded")
	ErrQuotaBytes   = stderrors.New("artifact: daily byte quota exceeded")
	ErrNotFound     = stderrors.New("artifact: not found")
	ErrSizeMismatch = stderrors.New("artifact: uploaded size does not match declared size")
	ErrBadRequest   = stderrors.New("artifact: bad request")
)

// maxS3Parts is the S3/B2 multipart hard cap.
const maxS3Parts = 10000

// Options tunes upload/download behaviour (sourced from config).
type Options struct {
	MultipartThreshold int64
	PartSize           int64
	PresignUploadTTL   time.Duration
	PresignDownloadTTL time.Duration
}

// Service handles artifact business logic.
type Service struct {
	repo  *repository.ArtifactRepository
	store *storage.Client
	quota *quota.Checker
	opts  Options
}

// New creates an artifact Service.
func New(repo *repository.ArtifactRepository, store *storage.Client, q *quota.Checker, opts Options) *Service {
	if opts.MultipartThreshold <= 0 {
		opts.MultipartThreshold = 50 * 1024 * 1024
	}
	if opts.PartSize <= 0 {
		opts.PartSize = 16 * 1024 * 1024
	}
	if opts.PresignUploadTTL <= 0 {
		opts.PresignUploadTTL = time.Hour
	}
	if opts.PresignDownloadTTL <= 0 {
		opts.PresignDownloadTTL = time.Hour
	}
	return &Service{repo: repo, store: store, quota: q, opts: opts}
}

// InitParams carries the authenticated caller + per-site limits into InitUpload.
type InitParams struct {
	Site           string
	UploaderSub    string
	UploaderClient string
	MaxFileSize    int64
	QuotaCount     int
	QuotaBytes     int64
	AllowedMime    []string // MIME types and/or extensions; empty = allow all
}

// InitUpload validates + reserves quota, creates the row (status=uploading) and
// returns presigned URL(s) for the client to upload directly to B2.
func (s *Service) InitUpload(ctx context.Context, req dto.InitUploadRequest, p InitParams) (*dto.InitUploadResponse, error) {
	if p.MaxFileSize > 0 && req.FileSize > p.MaxFileSize {
		return nil, ErrTooBig
	}
	if !mimeAllowed(req.MimeType, req.Name, p.AllowedMime) {
		return nil, ErrMIMEDenied
	}

	// Reserve quota up front (we never see the bytes; reserve on declared size).
	if s.quota != nil {
		if _, qerr := s.quota.Reserve(ctx, p.Site, req.FileSize, p.QuotaCount, p.QuotaBytes); qerr != nil {
			switch {
			case stderrors.Is(qerr, quota.ErrCountExceeded):
				return nil, ErrQuotaCount
			case stderrors.Is(qerr, quota.ErrBytesExceeded):
				return nil, ErrQuotaBytes
			case stderrors.Is(qerr, quota.ErrNotConfigured):
				// Redis not configured (dev) — skip quota.
			default:
				return nil, qerr
			}
		}
	}

	id := uuid.NewString()
	fileKey := p.Site + "/" + id + "/" + sanitizeName(req.Name)

	a := &model.Artifact{
		UUID:           id,
		SiteKey:        p.Site,
		UploaderSub:    p.UploaderSub,
		UploaderClient: p.UploaderClient,
		Name:           req.Name,
		Description:    req.Description,
		FileKey:        fileKey,
		ReportedSize:   req.FileSize,
		MimeType:       req.MimeType,
		Checksum:       req.Checksum,
		Status:         model.StatusUploading,
		Public:         req.Public,
	}
	// Persist the row BEFORE any B2 multipart so an interrupted init always
	// leaves a status=0 row the GC job can find and clean.
	if err := s.repo.Create(ctx, a); err != nil {
		return nil, err
	}

	expiresAt := time.Now().Add(s.opts.PresignUploadTTL).UTC().Format(time.RFC3339)
	resp := &dto.InitUploadResponse{UUID: id, ExpiresAt: expiresAt}

	if req.FileSize >= s.opts.MultipartThreshold {
		numParts := (req.FileSize + s.opts.PartSize - 1) / s.opts.PartSize
		if numParts > maxS3Parts {
			return nil, ErrTooBig
		}
		uploadID, err := s.store.CreateMultipart(ctx, fileKey)
		if err != nil {
			return nil, err
		}
		a.UploadID = uploadID
		a.PartSize = s.opts.PartSize
		if err := s.repo.Update(ctx, a); err != nil {
			return nil, err
		}
		parts := make([]dto.PartURL, 0, numParts)
		for n := int32(1); int64(n) <= numParts; n++ {
			url, err := s.store.PresignUploadPart(ctx, fileKey, uploadID, n, s.opts.PresignUploadTTL)
			if err != nil {
				return nil, err
			}
			parts = append(parts, dto.PartURL{PartNumber: n, URL: url})
		}
		resp.Multipart = true
		resp.UploadID = uploadID
		resp.PartSize = s.opts.PartSize
		resp.PartURLs = parts
		return resp, nil
	}

	url, err := s.store.PresignPut(ctx, fileKey, s.opts.PresignUploadTTL)
	if err != nil {
		return nil, err
	}
	resp.UploadURL = url
	return resp, nil
}

// CompleteUpload finalises an upload: completes multipart, verifies the actual
// size matches the declared size, persists status=ready and optional manifest.
func (s *Service) CompleteUpload(ctx context.Context, uuidStr, site string, req dto.CompleteUploadRequest) (*dto.ArtifactResponse, error) {
	a, err := s.repo.FindByUUID(ctx, uuidStr)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if a.SiteKey != site {
		return nil, ErrNotFound // don't leak cross-site existence
	}
	if a.IsReady() {
		return s.persistManifestAndRespond(ctx, a, req.Manifest) // idempotent re-complete
	}

	if a.IsMultipart() {
		if len(req.Parts) == 0 {
			return nil, ErrBadRequest
		}
		parts := make([]storage.CompletedPart, 0, len(req.Parts))
		for _, p := range req.Parts {
			parts = append(parts, storage.CompletedPart{PartNumber: p.PartNumber, ETag: p.ETag})
		}
		sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
		if err := s.store.CompleteMultipart(ctx, a.FileKey, a.UploadID, parts); err != nil {
			s.markFailed(ctx, a)
			return nil, err
		}
	}

	actual, err := s.store.HeadSize(ctx, a.FileKey)
	if err != nil {
		s.markFailed(ctx, a)
		return nil, err
	}
	if actual != a.ReportedSize {
		// Size doesn't match the declaration — delete the object and fail.
		if delErr := s.store.Delete(ctx, a.FileKey); delErr != nil {
			slog.Warn("artifact: delete on size mismatch failed", "uuid", a.UUID, "err", delErr)
		}
		s.markFailed(ctx, a)
		return nil, ErrSizeMismatch
	}

	a.FileSize = actual
	a.Status = model.StatusReady
	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}
	return s.persistManifestAndRespond(ctx, a, req.Manifest)
}

// Download returns a download URL for a ready artifact owned by the site.
// Public artifacts on a site with a Worker CDN base get the cacheable Worker
// URL; everything else gets a short-lived presigned GET.
func (s *Service) Download(ctx context.Context, uuidStr, site, cdnBase string) (*dto.DownloadResponse, error) {
	a, err := s.repo.FindByUUID(ctx, uuidStr)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if a.SiteKey != site || !a.IsReady() {
		return nil, ErrNotFound
	}

	if a.Public && cdnBase != "" {
		return &dto.DownloadResponse{URL: strings.TrimRight(cdnBase, "/") + "/" + a.FileKey}, nil
	}

	url, err := s.store.PresignGet(ctx, a.FileKey, a.Name, s.opts.PresignDownloadTTL)
	if err != nil {
		return nil, err
	}
	return &dto.DownloadResponse{
		URL:       url,
		ExpiresAt: time.Now().Add(s.opts.PresignDownloadTTL).UTC().Format(time.RFC3339),
	}, nil
}

// Delete soft-deletes a site's artifact (GC physically removes after the TTL).
func (s *Service) Delete(ctx context.Context, uuidStr, site string) (bool, error) {
	return s.repo.SoftDeleteByUUID(ctx, uuidStr, site)
}

// Get returns one artifact's metadata, scoped to the caller's site.
func (s *Service) Get(ctx context.Context, uuidStr, site string) (*dto.ArtifactResponse, error) {
	a, err := s.repo.FindByUUID(ctx, uuidStr)
	if err != nil {
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if a.SiteKey != site {
		return nil, ErrNotFound
	}
	return toResponse(a), nil
}

// List returns a page of the site's artifacts plus the total count.
func (s *Service) List(ctx context.Context, site string, offset, limit int) ([]dto.ArtifactResponse, int64, error) {
	items, total, err := s.repo.ListBySite(ctx, site, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dto.ArtifactResponse, 0, len(items))
	for i := range items {
		out = append(out, *toResponse(&items[i]))
	}
	return out, total, nil
}

// ---- helpers ----

func (s *Service) markFailed(ctx context.Context, a *model.Artifact) {
	a.Status = model.StatusFailed
	if err := s.repo.Update(ctx, a); err != nil {
		slog.Error("artifact: mark failed", "uuid", a.UUID, "err", err)
	}
}

func (s *Service) persistManifestAndRespond(ctx context.Context, a *model.Artifact, mi *dto.ManifestInput) (*dto.ArtifactResponse, error) {
	if mi != nil {
		m := &model.Manifest{
			ArtifactID: a.ID,
			Executable: mi.Executable,
			Arguments:  mi.Arguments,
			WorkingDir: mi.WorkingDir,
			SavePath:   mi.SavePath,
		}
		if mi.Requirements != nil {
			if raw, err := json.Marshal(mi.Requirements); err == nil {
				m.Requirements = datatypes.JSON(raw)
			}
		}
		if err := s.repo.SaveManifest(ctx, m); err != nil {
			return nil, err
		}
	}
	return toResponse(a), nil
}

func toResponse(a *model.Artifact) *dto.ArtifactResponse {
	return &dto.ArtifactResponse{
		UUID:        a.UUID,
		SiteKey:     a.SiteKey,
		Name:        a.Name,
		Description: a.Description,
		FileSize:    a.FileSize,
		MimeType:    a.MimeType,
		Checksum:    a.Checksum,
		Status:      a.Status,
		Public:      a.Public,
		CreatedAt:   a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// sanitizeName strips path separators so the filename can't break out of the
// {site}/{uuid}/ key prefix. Empty falls back to the uuid-less "file".
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	return name
}

// mimeAllowed reports whether the declared MIME or the filename extension is in
// the per-site allowlist. Empty allowlist = allow anything.
func mimeAllowed(mime, name string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if mime != "" && a == strings.ToLower(mime) {
			return true
		}
		if ext != "" && (a == ext || a == strings.TrimPrefix(ext, ".")) {
			return true
		}
	}
	return false
}
