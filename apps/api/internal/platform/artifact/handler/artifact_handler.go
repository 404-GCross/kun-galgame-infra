// Package handler implements the HTTP surface of the artifact service:
//
//	POST   /api/v1/artifacts                 — InitUpload
//	POST   /api/v1/artifacts/:uuid/complete  — CompleteUpload
//	GET    /api/v1/artifacts                  — List (site-scoped)
//	GET    /api/v1/artifacts/:uuid            — Get metadata
//	GET    /api/v1/artifacts/:uuid/download   — issue download URL
//	DELETE /api/v1/artifacts/:uuid            — soft delete
package handler

import (
	stderrors "errors"
	"log/slog"

	"api/internal/platform/artifact/dto"
	artMW "api/internal/platform/artifact/middleware"
	"api/internal/platform/artifact/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// Handler bundles the artifact handlers.
type Handler struct {
	svc *service.Service
}

// New creates an artifact Handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// InitUpload — POST /artifacts. Validates, reserves quota, returns presigned
// upload URL(s).
func (h *Handler) InitUpload(c fiber.Ctx) error {
	client := artMW.ClientFromCtx(c)
	site := artMW.SiteKeyFromCtx(c)
	if client == nil || site == "" {
		return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
	}

	var req dto.InitUploadRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}

	uploaderSub := artMW.UserSubFromCtx(c)
	if uploaderSub == "" {
		uploaderSub = req.UploaderSub
	}

	resp, err := h.svc.InitUpload(c.Context(), req, service.InitParams{
		Site:           site,
		UploaderSub:    uploaderSub,
		UploaderClient: client.ID,
		MaxFileSize:    client.ArtifactMaxFileSize,
		QuotaCount:     client.ArtifactQuotaDaily,
		QuotaBytes:     client.ArtifactQuotaBytesDaily,
		AllowedMime:    client.ArtifactAllowedMimes(),
	})
	if err != nil {
		switch {
		case stderrors.Is(err, service.ErrTooBig):
			return response.Error(c, fiber.StatusRequestEntityTooLarge, errors.ErrArtifactTooBig, errors.GetMessage(errors.ErrArtifactTooBig))
		case stderrors.Is(err, service.ErrMIMEDenied):
			return response.BadRequest(c, errors.ErrArtifactMIMEDenied)
		case stderrors.Is(err, service.ErrQuotaCount), stderrors.Is(err, service.ErrQuotaBytes):
			return response.Error(c, fiber.StatusTooManyRequests, errors.ErrArtifactQuotaExceeded, errors.GetMessage(errors.ErrArtifactQuotaExceeded))
		default:
			slog.Error("artifact init", "site", site, "client_id", client.ID, "err", err)
			return response.InternalError(c, errors.ErrArtifactStoreFailed)
		}
	}
	return response.Success(c, resp)
}

// CompleteUpload — POST /artifacts/:uuid/complete.
func (h *Handler) CompleteUpload(c fiber.Ctx) error {
	site := artMW.SiteKeyFromCtx(c)
	if site == "" {
		return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
	}
	id := c.Params("uuid")
	if id == "" {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}

	var req dto.CompleteUploadRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}

	resp, err := h.svc.CompleteUpload(c.Context(), id, site, req)
	if err != nil {
		switch {
		case stderrors.Is(err, service.ErrNotFound):
			return response.NotFound(c, errors.ErrArtifactNotFound)
		case stderrors.Is(err, service.ErrBadRequest):
			return response.BadRequest(c, errors.ErrArtifactBadRequest)
		case stderrors.Is(err, service.ErrSizeMismatch):
			return response.BadRequest(c, errors.ErrArtifactSizeMismatch)
		default:
			slog.Error("artifact complete", "uuid", id, "site", site, "err", err)
			return response.InternalError(c, errors.ErrArtifactStoreFailed)
		}
	}
	return response.Success(c, resp)
}

// Download — GET /artifacts/:uuid/download. Returns a presigned GET URL (or a
// Worker URL for public artifacts on a site with a CDN base configured).
func (h *Handler) Download(c fiber.Ctx) error {
	client := artMW.ClientFromCtx(c)
	site := artMW.SiteKeyFromCtx(c)
	if client == nil || site == "" {
		return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
	}
	id := c.Params("uuid")
	if id == "" {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}

	resp, err := h.svc.Download(c.Context(), id, site, client.ArtifactCDNBase)
	if err != nil {
		if stderrors.Is(err, service.ErrNotFound) {
			return response.NotFound(c, errors.ErrArtifactNotFound)
		}
		slog.Error("artifact download", "uuid", id, "site", site, "err", err)
		return response.InternalError(c, errors.ErrArtifactStoreFailed)
	}
	return response.Success(c, resp)
}

// Get — GET /artifacts/:uuid.
func (h *Handler) Get(c fiber.Ctx) error {
	site := artMW.SiteKeyFromCtx(c)
	if site == "" {
		return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
	}
	id := c.Params("uuid")
	if id == "" {
		return response.BadRequest(c, errors.ErrArtifactBadRequest)
	}
	resp, err := h.svc.Get(c.Context(), id, site)
	if err != nil {
		if stderrors.Is(err, service.ErrNotFound) {
			return response.NotFound(c, errors.ErrArtifactNotFound)
		}
		slog.Error("artifact get", "uuid", id, "site", site, "err", err)
		return response.InternalError(c, errors.ErrArtifactStoreFailed)
	}
	return response.Success(c, resp)
}

// List — GET /artifacts (site-scoped, paginated).
func (h *Handler) List(c fiber.Ctx) error {
	site := artMW.SiteKeyFromCtx(c)
	if site == "" {
		return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
	}
	var p utils.Pagination
	if err := c.Bind().Query(&p); err != nil {
		p = utils.DefaultPagination()
	}
	p.Normalize()

	items, total, err := h.svc.List(c.Context(), site, p.Offset(), p.Limit())
	if err != nil {
		slog.Error("artifact list", "site", site, "err", err)
		return response.InternalError(c, errors.ErrArtifactStoreFailed)
	}
	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// Delete — DELETE /artifacts/:uuid (soft delete; GC reclaims after TTL).
func (h *Handler) Delete(c fiber.Ctx) error {
	site := artMW.SiteKeyFromCtx(c)
	if site == "" {
		return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
	}
	id := c.Params("uuid")
	ok, err := h.svc.Delete(c.Context(), id, site)
	if err != nil {
		slog.Error("artifact delete", "uuid", id, "site", site, "err", err)
		return response.InternalError(c, errors.ErrArtifactStoreFailed)
	}
	if !ok {
		return response.NotFound(c, errors.ErrArtifactNotFound)
	}
	return response.Success(c, fiber.Map{"uuid": id, "deleted": true})
}
