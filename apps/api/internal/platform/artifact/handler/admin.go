package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"api/internal/platform/artifact/dto"
	"api/internal/platform/artifact/model"
	"api/internal/platform/artifact/repository"
	"api/internal/platform/artifact/storage"

	"gorm.io/gorm"
)

// AdminHandler holds the dependencies for the admin-only artifact operations
// (served over Huma by SetupAdmin, mounted in the OAuth service behind
// Auth + the oauth.admin_access gate — see cmd/oauth). Its methods are transport-free
// (context in, typed data / sentinel errors out); the Huma layer in
// admin_huma.go maps them to the house envelope.
//
// `store` is the least-privilege cleanup S3 client (same one the GC uses,
// built from ArtifactCleanupS3). It may be nil when the oauth process has no
// artifact object-storage credentials configured — in that case Reclaim is
// unavailable (503) and only the soft-delete path works, exactly like before.
type AdminHandler struct {
	db             *gorm.DB
	statsRepo      *repository.StatsRepository
	store          *storage.Client
	reclaimMinIdle time.Duration
}

func NewAdmin(db *gorm.DB, statsRepo *repository.StatsRepository, store *storage.Client, reclaimMinIdle time.Duration) *AdminHandler {
	if reclaimMinIdle <= 0 {
		reclaimMinIdle = time.Hour
	}
	return &AdminHandler{db: db, statsRepo: statsRepo, store: store, reclaimMinIdle: reclaimMinIdle}
}

// Sentinel errors — the Huma layer maps these to HTTP status + house code.
var (
	errAdminNotFound   = errors.New("artifact not found")
	errAdminBadRequest = errors.New("bad request")
	// Reclaim-specific conflicts (all map to 409 except unavailable → 503).
	errReclaimUnavailable  = errors.New("artifact storage not configured; reclaim unavailable")
	errReclaimNotUploading = errors.New("only in-progress (uploading) artifacts can be reclaimed")
	errReclaimActive       = errors.New("upload still active; refusing to interrupt")
	errReclaimRaced        = errors.New("upload changed state; not reclaimed")
)

var statusLabels = map[int]string{
	model.StatusUploading: "uploading",
	model.StatusReady:     "ready",
	model.StatusFailed:    "failed",
}

// AdminListFilters is the parsed, validated filter set for List (the Huma layer
// parses the raw query string into this).
type AdminListFilters struct {
	Site        string
	Status      string // "" | uploading | ready | failed
	UploaderSub string
	Search      string
	From        *time.Time // created_at >=
	To          *time.Time // created_at <=
	Page        int
	Limit       int
}

// List paginates the artifacts table with optional filters.
func (h *AdminHandler) List(ctx context.Context, f AdminListFilters) (dto.AdminArtifactList, error) {
	q := h.db.WithContext(ctx).Model(&model.Artifact{})

	if f.Site != "" {
		q = q.Where("site_key = ?", f.Site)
	}
	switch f.Status {
	case "":
		// no status filter
	case "uploading":
		q = q.Where("status = ?", model.StatusUploading)
	case "ready":
		q = q.Where("status = ?", model.StatusReady)
	case "failed":
		q = q.Where("status = ?", model.StatusFailed)
	default:
		return dto.AdminArtifactList{}, errAdminBadRequest
	}
	if f.UploaderSub != "" {
		q = q.Where("uploader_sub = ?", f.UploaderSub)
	}
	if f.Search != "" {
		// Match by filename substring or exact UUID.
		q = q.Where("name ILIKE ? OR uuid::text = ?", "%"+f.Search+"%", f.Search)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	limit := f.Limit
	if limit < 1 || limit > 200 {
		limit = 50
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return dto.AdminArtifactList{}, fmt.Errorf("count artifacts: %w", err)
	}

	var rows []model.Artifact
	if err := q.Order("created_at DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&rows).Error; err != nil {
		return dto.AdminArtifactList{}, fmt.Errorf("query artifacts: %w", err)
	}

	items := make([]dto.AdminArtifactRow, 0, len(rows))
	for i := range rows {
		items = append(items, toAdminRow(&rows[i]))
	}
	return dto.AdminArtifactList{Items: items, Total: total, Page: page, Limit: limit}, nil
}

// toAdminRow shapes an Artifact for admin list consumers (no S3 presigning).
func toAdminRow(a *model.Artifact) dto.AdminArtifactRow {
	return dto.AdminArtifactRow{
		UUID:           a.UUID,
		Name:           a.Name,
		FileKey:        a.FileKey,
		FileSize:       a.FileSize,
		MimeType:       a.MimeType,
		SiteKey:        a.SiteKey,
		Status:         statusLabels[a.Status],
		Public:         a.Public,
		UploaderSub:    a.UploaderSub,
		UploaderClient: a.UploaderClient,
		Checksum:       a.Checksum,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
}

// Stats returns aggregate counters across all sites.
func (h *AdminHandler) Stats(ctx context.Context) (dto.AdminArtifactStats, error) {
	res, err := h.statsRepo.Stats(ctx)
	if err != nil {
		return dto.AdminArtifactStats{}, err
	}
	out := dto.AdminArtifactStats{
		TotalCount:  res.TotalCount,
		TotalBytes:  res.TotalBytes,
		Uploading:   res.Uploading,
		Failed:      res.Failed,
		SoftDeleted: res.SoftDeleted,
	}
	if len(res.BySite) > 0 {
		out.BySite = make(map[string]dto.AdminArtifactSiteStats, len(res.BySite))
		for k, v := range res.BySite {
			out.BySite[k] = dto.AdminArtifactSiteStats{Count: v.Count, Bytes: v.Bytes}
		}
	}
	return out, nil
}

// Delete soft-deletes an artifact (sets deleted_at). The artifact GC job
// physically removes the B2 object after the soft-delete TTL — there is no
// hard-delete path here because the OAuth admin service is not provisioned with
// artifact object-storage credentials.
func (h *AdminHandler) Delete(ctx context.Context, uuid string) error {
	if uuid == "" {
		return errAdminBadRequest
	}
	res := h.db.WithContext(ctx).
		Where("uuid = ?", uuid).
		Delete(&model.Artifact{})
	if res.Error != nil {
		return fmt.Errorf("soft-delete artifact: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errAdminNotFound
	}
	return nil
}

// Reclaim immediately frees an INTERRUPTED upload (status=uploading) that the
// normal soft-delete path would mishandle: it aborts the dangling B2 multipart
// (otherwise its parts linger, billed, never reclaimed), deletes any object,
// and hard-drops the row. This is the orphan-GC reclaim logic, manually
// triggered for a single uuid — for when you can see a stuck upload and don't
// want to wait for the 24h sweep.
//
// Safety (this is a destructive op):
//   - status MUST be uploading. ready/failed are refused (use soft-delete) so
//     this can never touch a live, downloadable artifact.
//   - idle guard: refuse if updated_at is newer than reclaimMinIdle, so an
//     actively-uploading or just-resumed upload isn't interrupted.
//   - claim-FIRST: the row is conditionally hard-deleted (WHERE status=uploading)
//     BEFORE any S3 call. If a concurrent Complete already flipped it to ready,
//     the conditional delete affects 0 rows and we bail WITHOUT touching storage
//     — so a just-completed object is never deleted. Once we own the row, a
//     concurrent Complete's FindByUUID misses and fails cleanly.
//
// Requires the cleanup S3 client (oauth must have artifact storage creds, same
// as the GC). Without it → errReclaimUnavailable (503).
func (h *AdminHandler) Reclaim(ctx context.Context, uuid string) error {
	if h.store == nil {
		return errReclaimUnavailable
	}
	if uuid == "" {
		return errAdminBadRequest
	}

	var a model.Artifact
	if err := h.db.WithContext(ctx).Where("uuid = ?", uuid).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errAdminNotFound
		}
		return fmt.Errorf("reclaim load: %w", err)
	}
	if a.Status != model.StatusUploading {
		// ready/failed are not "in-progress"; soft-delete handles those.
		return errReclaimNotUploading
	}
	if idle := time.Since(a.UpdatedAt); idle < h.reclaimMinIdle {
		return fmt.Errorf("%w (idle %s < min %s)", errReclaimActive, idle.Round(time.Second), h.reclaimMinIdle)
	}

	// Claim the row first (conditional on it still being uploading). This both
	// makes double-reclaim safe and prevents racing a concurrent Complete.
	claim := h.db.WithContext(ctx).Unscoped().
		Where("uuid = ? AND status = ?", uuid, model.StatusUploading).
		Delete(&model.Artifact{})
	if claim.Error != nil {
		return fmt.Errorf("reclaim claim: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		// Raced: it just completed, failed, or another reclaim won. Don't touch S3.
		return errReclaimRaced
	}

	// We own the (now-deleted) row. Free storage best-effort: a transient failure
	// at worst leaks an INCOMPLETE multipart (no live object — the row was
	// uploading), recoverable via B2 lifecycle; it can never delete a finished
	// object (that path was excluded by the conditional claim above).
	if a.UploadID != "" {
		if err := h.store.AbortMultipart(ctx, a.FileKey, a.UploadID); err != nil {
			slog.Warn("artifact reclaim: abort multipart", "uuid", uuid, "key", a.FileKey, "err", err)
		}
	}
	if err := h.store.Delete(ctx, a.FileKey); err != nil {
		slog.Warn("artifact reclaim: delete object", "uuid", uuid, "key", a.FileKey, "err", err)
	}
	return nil
}
