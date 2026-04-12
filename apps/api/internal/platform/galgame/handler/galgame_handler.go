package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// GalgameHandler handles galgame HTTP requests
type GalgameHandler struct {
	galgameService *service.GalgameService
}

// NewGalgameHandler creates a new GalgameHandler
func NewGalgameHandler(galgameService *service.GalgameService) *GalgameHandler {
	return &GalgameHandler{galgameService: galgameService}
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

// Create creates a new galgame
func (h *GalgameHandler) Create(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	var req dto.CreateGalgameRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
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

	return response.Success(c, galgame)
}

// Update updates an existing galgame
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
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
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

	return response.Success(c, galgame)
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
