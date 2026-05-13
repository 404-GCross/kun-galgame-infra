package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/search"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/imageclient"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// GalgameHandler handles galgame HTTP requests
type GalgameHandler struct {
	galgameService *service.GalgameService
	searchHook     *search.Hook        // optional write-through to Meilisearch
	imgClient      *imageclient.Client // optional; nil disables banner upload via multipart
}

// NewGalgameHandler creates a new GalgameHandler.
// Pass nil for hook to disable write-through (e.g. in tests).
// Pass nil for imgClient to disable banner-file upload via multipart;
// consumers must then send banner_image_hash via JSON directly.
func NewGalgameHandler(galgameService *service.GalgameService, hook *search.Hook, imgClient *imageclient.Client) *GalgameHandler {
	return &GalgameHandler{
		galgameService: galgameService,
		searchHook:     hook,
		imgClient:      imgClient,
	}
}

// List returns a paginated list of galgames
func (h *GalgameHandler) List(c fiber.Ctx) error {
	var req dto.ListGalgameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	items, total, err := h.galgameService.List(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
	})
}

// Get returns a galgame by ID with all relations
func (h *GalgameHandler) Get(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	galgame, users, err := h.galgameService.GetByID(c.Context(), id)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"galgame": galgame,
		"users":   users,
	})
}

// Create creates a new galgame.
//
// Accepts both JSON body and multipart/form-data (with optional `file` field).
// In multipart mode, the file is uploaded to image_service first; the
// resulting hash is recorded as banner_image_hash on the new galgame.
func (h *GalgameHandler) Create(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	var req dto.CreateGalgameRequest
	bannerHash, err := parseGalgameWriteBody(c, h.imgClient, &req)
	if err != nil {
		return mapWriteBodyError(c, err)
	}
	if bannerHash != "" {
		req.BannerImageHash = bannerHash
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	galgame, err := h.galgameService.Create(c.Context(), int(uid), &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Galgame(galgame.ID)

	return response.Success(c, galgame)
}

// Update updates an existing galgame.
//
// Accepts both JSON body and multipart/form-data (with optional `file`
// banner). In multipart mode, the file is uploaded to image_service first
// and the resulting hash is treated as a normal banner_image_hash field
// change — going through the standard revision/PR pipeline.
func (h *GalgameHandler) Update(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.UpdateGalgameRequest
	bannerHash, err := parseGalgameWriteBody(c, h.imgClient, &req)
	if err != nil {
		return mapWriteBodyError(c, err)
	}
	if bannerHash != "" {
		req.BannerImageHash = &bannerHash
	}

	galgame, err := h.galgameService.Update(c.Context(), int(uid), id, roles, &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			switch appErr.Code {
			case errors.ErrGalgameNotFound:
				return response.NotFound(c, appErr.Code)
			case errors.ErrGalgameForbidden:
				return response.Forbidden(c, appErr.Code)
			default:
				return response.BadRequest(c, appErr.Code)
			}
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Galgame(id)

	return response.Success(c, galgame)
}

// BatchGet returns lightweight galgame info for a list of IDs.
//
// Public by default (status=0 only). When an OptionalJWT middleware has
// populated user_uid in Locals, the response also includes the caller's
// own status=3/4 entries — matching the visibility rules in
// docs/galgame_wiki/06-submission-and-review-design.md §6.
func (h *GalgameHandler) BatchGet(c fiber.Ctx) error {
	var req dto.BatchGetGalgameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if len(req.IDs) == 0 || len(req.IDs) > 100 {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "ids must contain 1-100 items")
	}

	viewerUID, _ := c.Locals("user_uid").(uint)
	items, err := h.galgameService.BatchGetWithViewer(c.Context(), req.IDs, int(viewerUID))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, items)
}

// UserStats returns aggregated galgame statistics for a user
func (h *GalgameHandler) UserStats(c fiber.Ctx) error {
	uid, err := strconv.Atoi(c.Params("uid"))
	if err != nil || uid <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	stats, err := h.galgameService.GetUserStats(c.Context(), uid)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, stats)
}

// CheckVNDB checks if a VNDB ID already exists
func (h *GalgameHandler) CheckVNDB(c fiber.Ctx) error {
	var req dto.CheckVNDBRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	exists, galgameID, err := h.galgameService.CheckVNDB(c.Context(), req.VNDBID)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	data := fiber.Map{"exists": exists}
	if exists {
		data["galgame_id"] = galgameID
	}

	return response.Success(c, data)
}
