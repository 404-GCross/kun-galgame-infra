package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// AdminHandler handles admin statistics requests
type AdminHandler struct {
	adminRepo *repository.AdminRepository
}

// NewAdminHandler creates a new AdminHandler
func NewAdminHandler(adminRepo *repository.AdminRepository) *AdminHandler {
	return &AdminHandler{adminRepo: adminRepo}
}

// Stats returns wiki management statistics
func (h *AdminHandler) Stats(c fiber.Ctx) error {
	var req dto.AdminStatsRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Days <= 0 {
		req.Days = 30
	}
	if req.Days > 365 {
		req.Days = 365
	}

	stats, err := h.adminRepo.GetStats(c.Context(), req.Days)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, stats)
}

// ListGalgames returns a paginated list of galgames with optional status filter.
func (h *AdminHandler) ListGalgames(c fiber.Ctx) error {
	var req dto.AdminListGalgamesRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page < 1 {
		req.Page = 1
	}
	if req.Limit < 1 {
		req.Limit = 20
	}
	if req.Limit > 100 {
		req.Limit = 100
	}

	items, total, err := h.adminRepo.ListGalgames(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
	})
}

// GetGalgame returns a galgame by id with full relations (any status).
func (h *AdminHandler) GetGalgame(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	galgame, err := h.adminRepo.GetGalgame(c.Context(), id)
	if err != nil {
		return response.NotFound(c, errors.ErrGalgameNotFound)
	}

	return response.Success(c, galgame)
}

// UpdateGalgameStatus changes the status of a galgame.
func (h *AdminHandler) UpdateGalgameStatus(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.AdminUpdateGalgameStatusRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Status != 0 && req.Status != 1 && req.Status != 2 {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := h.adminRepo.UpdateGalgameStatus(c.Context(), id, req.Status); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"id": id, "status": req.Status})
}
