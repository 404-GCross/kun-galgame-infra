package handler

import (
	"log/slog"
	"strconv"
	"time"

	"api/internal/platform/artifact/model"
	"api/internal/platform/artifact/repository"
	"api/internal/platform/artifact/storage"
	errs "api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// AdminHandler bundles admin-only artifact endpoints. Mounted behind
// middleware.Auth + middleware.RequireRole(...) in the OAuth admin service
// (not cmd/artifact itself).
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

var statusLabels = map[int]string{
	model.StatusUploading: "uploading",
	model.StatusReady:     "ready",
	model.StatusFailed:    "failed",
}

// ---- GET /admin/artifact/list ----

// List paginates the artifacts table with optional filters.
func (h *AdminHandler) List(c fiber.Ctx) error {
	q := h.db.WithContext(c.Context()).Model(&model.Artifact{})

	if site := c.Query("site"); site != "" {
		q = q.Where("site_key = ?", site)
	}
	if status := c.Query("status"); status != "" {
		switch status {
		case "uploading":
			q = q.Where("status = ?", model.StatusUploading)
		case "ready":
			q = q.Where("status = ?", model.StatusReady)
		case "failed":
			q = q.Where("status = ?", model.StatusFailed)
		default:
			return response.BadRequest(c, errs.ErrBadRequest)
		}
	}
	if sub := c.Query("uploader_sub"); sub != "" {
		q = q.Where("uploader_sub = ?", sub)
	}
	if s := c.Query("search"); s != "" {
		// Match by filename substring or exact UUID.
		q = q.Where("name ILIKE ? OR uuid::text = ?", "%"+s+"%", s)
	}
	if from := c.Query("from"); from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return response.BadRequest(c, errs.ErrBadRequest)
		}
		q = q.Where("created_at >= ?", t)
	}
	if to := c.Query("to"); to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return response.BadRequest(c, errs.ErrBadRequest)
		}
		q = q.Where("created_at <= ?", t)
	}

	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		slog.Error("artifact admin list count", "err", err)
		return response.InternalError(c, errs.ErrInternalServer)
	}

	var rows []model.Artifact
	if err := q.Order("created_at DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&rows).Error; err != nil {
		slog.Error("artifact admin list query", "err", err)
		return response.InternalError(c, errs.ErrInternalServer)
	}

	items := make([]map[string]any, 0, len(rows))
	for i := range rows {
		items = append(items, toAdminRow(&rows[i]))
	}
	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// toAdminRow shapes an Artifact for admin list consumers (no S3 presigning).
func toAdminRow(a *model.Artifact) map[string]any {
	return map[string]any{
		"uuid":            a.UUID,
		"name":            a.Name,
		"file_key":        a.FileKey,
		"file_size":       a.FileSize,
		"mime_type":       a.MimeType,
		"site_key":        a.SiteKey,
		"status":          statusLabels[a.Status],
		"public":          a.Public,
		"uploader_sub":    a.UploaderSub,
		"uploader_client": a.UploaderClient,
		"checksum":        a.Checksum,
		"created_at":      a.CreatedAt,
		// updated_at = last progress (init / multipart persist / resume Touch);
		// for status=uploading rows it's the "idle since" signal the UI shows and
		// Reclaim guards on.
		"updated_at": a.UpdatedAt,
	}
}

// ---- GET /admin/artifact/stats ----

// Stats returns aggregate counters across all sites.
func (h *AdminHandler) Stats(c fiber.Ctx) error {
	res, err := h.statsRepo.Stats(c.Context())
	if err != nil {
		slog.Error("artifact admin stats", "err", err)
		return response.InternalError(c, errs.ErrInternalServer)
	}
	return response.Success(c, res)
}

// ---- DELETE /admin/artifact/:uuid ----

// Delete soft-deletes an artifact (sets deleted_at). The artifact GC job
// physically removes the B2 object after the soft-delete TTL — there is no
// hard-delete path here because the OAuth admin service is not provisioned with
// artifact object-storage credentials.
func (h *AdminHandler) Delete(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errs.ErrBadRequest)
	}
	res := h.db.WithContext(c.Context()).
		Where("uuid = ?", uuid).
		Delete(&model.Artifact{})
	if res.Error != nil {
		slog.Error("artifact admin delete", "err", res.Error)
		return response.InternalError(c, errs.ErrInternalServer)
	}
	if res.RowsAffected == 0 {
		return response.NotFound(c, errs.ErrNotFound)
	}
	return response.Success(c, fiber.Map{"uuid": uuid, "soft_deleted": true})
}

// ---- POST /admin/artifact/:uuid/reclaim ----

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
// as the GC). Without it → 503.
func (h *AdminHandler) Reclaim(c fiber.Ctx) error {
	if h.store == nil {
		return response.Error(c, fiber.StatusServiceUnavailable, errs.ErrOperationFailed, "artifact storage not configured; reclaim unavailable")
	}
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errs.ErrBadRequest)
	}

	var a model.Artifact
	if err := h.db.WithContext(c.Context()).Where("uuid = ?", uuid).First(&a).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return response.NotFound(c, errs.ErrNotFound)
		}
		slog.Error("artifact reclaim: load", "uuid", uuid, "err", err)
		return response.InternalError(c, errs.ErrInternalServer)
	}
	if a.Status != model.StatusUploading {
		// ready/failed are not "in-progress"; soft-delete handles those.
		return response.Error(c, fiber.StatusConflict, errs.ErrBadRequest, "only in-progress (uploading) artifacts can be reclaimed")
	}
	if idle := time.Since(a.UpdatedAt); idle < h.reclaimMinIdle {
		return response.Error(c, fiber.StatusConflict, errs.ErrBadRequest,
			"upload still active (idle "+idle.Round(time.Second).String()+" < min "+h.reclaimMinIdle.String()+"); refusing to interrupt")
	}

	// Claim the row first (conditional on it still being uploading). This both
	// makes double-reclaim safe and prevents racing a concurrent Complete.
	claim := h.db.WithContext(c.Context()).Unscoped().
		Where("uuid = ? AND status = ?", uuid, model.StatusUploading).
		Delete(&model.Artifact{})
	if claim.Error != nil {
		slog.Error("artifact reclaim: claim", "uuid", uuid, "err", claim.Error)
		return response.InternalError(c, errs.ErrInternalServer)
	}
	if claim.RowsAffected == 0 {
		// Raced: it just completed, failed, or another reclaim won. Don't touch S3.
		return response.Error(c, fiber.StatusConflict, errs.ErrBadRequest, "upload changed state; not reclaimed")
	}

	// We own the (now-deleted) row. Free storage best-effort: a transient failure
	// at worst leaks an INCOMPLETE multipart (no live object — the row was
	// uploading), recoverable via B2 lifecycle; it can never delete a finished
	// object (that path was excluded by the conditional claim above).
	if a.UploadID != "" {
		if err := h.store.AbortMultipart(c.Context(), a.FileKey, a.UploadID); err != nil {
			slog.Warn("artifact reclaim: abort multipart", "uuid", uuid, "key", a.FileKey, "err", err)
		}
	}
	if err := h.store.Delete(c.Context(), a.FileKey); err != nil {
		slog.Warn("artifact reclaim: delete object", "uuid", uuid, "key", a.FileKey, "err", err)
	}
	return response.Success(c, fiber.Map{"uuid": uuid, "reclaimed": true})
}
