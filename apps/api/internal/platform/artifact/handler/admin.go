package handler

import (
	"log/slog"
	"strconv"
	"time"

	"api/internal/platform/artifact/model"
	"api/internal/platform/artifact/repository"
	errs "api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// AdminHandler bundles admin-only artifact endpoints. Mounted behind
// middleware.Auth + middleware.RequireRole("admin") in the OAuth admin service
// (not cmd/artifact itself), which is why it only needs a DB handle.
type AdminHandler struct {
	db        *gorm.DB
	statsRepo *repository.StatsRepository
}

func NewAdmin(db *gorm.DB, statsRepo *repository.StatsRepository) *AdminHandler {
	return &AdminHandler{db: db, statsRepo: statsRepo}
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
