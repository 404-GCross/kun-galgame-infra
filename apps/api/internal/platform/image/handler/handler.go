// Package handler implements the HTTP surface of the image service:
// - POST /image/upload
// - GET  /image/:hash
// - POST /image/reference-ping
package handler

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"log/slog"

	imgMW "api/internal/platform/image/middleware"
	"api/internal/platform/image/quota"
	"api/internal/platform/image/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// Handler bundles the image service handlers.
type Handler struct {
	svc   *service.Service
	quota *quota.Checker
}

func New(svc *service.Service, q *quota.Checker) *Handler {
	return &Handler{svc: svc, quota: q}
}

// ---- POST /image/upload ----

// Upload handles the multipart upload flow. Auth middleware has already
// validated the client and written OAuthClient + SiteKey to Locals.
func (h *Handler) Upload(c fiber.Ctx) error {
	client := imgMW.ClientFromCtx(c)
	site := imgMW.SiteKeyFromCtx(c)
	if client == nil || site == "" {
		return response.Unauthorized(c, errors.ErrImageUnauthorized)
	}

	presetName := c.FormValue("preset")
	if presetName == "" {
		return response.BadRequest(c, errors.ErrImageBadRequest)
	}
	if !client.IsPresetAllowed(presetName) {
		return response.Forbidden(c, errors.ErrImagePresetDenied)
	}

	fileHeader, err := c.FormFile("file")
	if err != nil || fileHeader == nil {
		return response.BadRequest(c, errors.ErrImageBadRequest)
	}

	// Per-site single-file size limit enforced before we read the body.
	if client.ImageMaxFileSize > 0 && fileHeader.Size > client.ImageMaxFileSize {
		return response.Error(c, fiber.StatusRequestEntityTooLarge, errors.ErrImageFileTooLarge, errors.GetMessage(errors.ErrImageFileTooLarge))
	}

	// Read the multipart body into memory. Bounded by ImageMaxFileSize
	// check above (and Fiber's global body limit — configured at app level).
	fh, err := fileHeader.Open()
	if err != nil {
		return response.InternalError(c, errors.ErrImageBadRequest)
	}
	defer fh.Close()
	body, err := io.ReadAll(fh)
	if err != nil {
		return response.InternalError(c, errors.ErrImageBadRequest)
	}

	// Quota reservation. Both count and bytes must fit.
	if h.quota != nil {
		usage, qerr := h.quota.Reserve(c.Context(), site, int64(len(body)), client.ImageQuotaDaily, client.ImageQuotaBytesDaily)
		if qerr != nil {
			if stderrors.Is(qerr, quota.ErrCountExceeded) || stderrors.Is(qerr, quota.ErrBytesExceeded) {
				details := fiber.Map{
					"quota_count": usage.LimitCount,
					"quota_bytes": usage.LimitBytes,
					"used_count":  usage.Count,
					"used_bytes":  usage.Bytes,
					"reset_at":    usage.ResetAt,
				}
				return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
					"code":    errors.ErrImageQuotaExceeded,
					"message": errors.GetMessage(errors.ErrImageQuotaExceeded),
					"details": details,
				})
			}
			if !stderrors.Is(qerr, quota.ErrNotConfigured) {
				slog.Error("quota reserve", "err", qerr)
				return response.InternalError(c, errors.ErrImageStoreFailed)
			}
			// Redis not configured: skip quota in dev.
		}
	}

	req := service.UploadRequest{
		Body:           body,
		Preset:         presetName,
		Site:           site,
		UploaderSub:    c.FormValue("uploader_sub"),
		UploaderClient: client.ID,
		UploaderIP:     c.IP(),
	}
	result, err := h.svc.Upload(c.Context(), req)
	if err != nil {
		switch {
		case stderrors.Is(err, service.ErrPresetNotFound):
			return response.BadRequest(c, errors.ErrImagePresetNotFound)
		case stderrors.Is(err, service.ErrMIMENotAllowed):
			return response.BadRequest(c, errors.ErrImageMIMEDenied)
		default:
			slog.Error("image upload failed",
				"site", site, "client_id", client.ID, "preset", presetName, "err", err)
			return response.InternalError(c, errors.ErrImageStoreFailed)
		}
	}

	return response.Success(c, result)
}

// ---- GET /image/:hash ----

// Meta returns the full metadata for a hash. Exposes `sites` audit info
// (available to any authenticated caller; consider restricting in V2 if
// cross-site probing becomes a concern).
func (h *Handler) Meta(c fiber.Ctx) error {
	hash := c.Params("hash")
	if len(hash) != 64 {
		return response.BadRequest(c, errors.ErrImageBadRequest)
	}

	img, sites, err := h.svc.GetByHash(c.Context(), hash)
	if err != nil {
		slog.Error("meta lookup failed", "hash", hash, "err", err)
		return response.InternalError(c, errors.ErrImageStoreFailed)
	}
	if img == nil {
		return response.NotFound(c, errors.ErrImageNotFound)
	}

	variantURLs := make(map[string]string, len(img.VariantList()))
	for _, v := range img.VariantList() {
		variantURLs[v] = h.svc.VariantURL(img.Hash, v, img.Ext)
	}

	return response.Success(c, fiber.Map{
		"hash":               img.Hash,
		"url":                h.svc.MainURL(img.Hash, img.Ext),
		"variant_urls":       variantURLs,
		"width":              img.Width,
		"height":             img.Height,
		"size_bytes":         img.SizeBytes,
		"mime":               img.MIME,
		"review_status":      reviewStatusLabel(img.ReviewStatus),
		"review_labels":      decodeReviewLabels(img.ReviewLabels),
		"created_at":         img.CreatedAt,
		"last_referenced_at": img.LastReferencedAt,
		"sites":              sites,
	})
}

// ---- POST /image/reference-ping ----

// referencePingRequest is accepted as JSON.
type referencePingRequest struct {
	Hashes []string `json:"hashes"`
}

// Ping refreshes last_referenced_at for a batch of hashes.
func (h *Handler) Ping(c fiber.Ctx) error {
	var req referencePingRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrImageBadRequest)
	}
	if len(req.Hashes) == 0 {
		return response.Success(c, fiber.Map{"updated": 0, "not_found": []string{}})
	}
	if len(req.Hashes) > 1000 {
		return response.BadRequest(c, errors.ErrImageBadRequest)
	}
	// Filter out obviously bad hashes.
	cleaned := make([]string, 0, len(req.Hashes))
	for _, h := range req.Hashes {
		if len(h) == 64 {
			cleaned = append(cleaned, h)
		}
	}

	updated, notFound, err := h.svc.ReferencePing(c.Context(), cleaned)
	if err != nil {
		slog.Error("reference-ping failed", "err", err)
		return response.InternalError(c, errors.ErrImageStoreFailed)
	}
	return response.Success(c, fiber.Map{
		"updated":   updated,
		"not_found": notFound,
	})
}

// ---- helpers ----

func reviewStatusLabel(s int16) string {
	switch s {
	case 0:
		return "pending"
	case 1:
		return "approved"
	case 2:
		return "rejected"
	case 3:
		return "manual_review"
	default:
		return "unknown"
	}
}

func decodeReviewLabels(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
