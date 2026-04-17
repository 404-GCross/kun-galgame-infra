package handler

import (
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
