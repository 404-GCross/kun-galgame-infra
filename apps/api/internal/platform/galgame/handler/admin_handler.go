package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/search"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// AdminHandler handles admin statistics + status mutation requests.
type AdminHandler struct {
	adminRepo  *repository.AdminRepository
	adminSvc   *service.AdminService
	searchHook *search.Hook
}

// NewAdminHandler creates an AdminHandler.
// adminSvc is required for UpdateGalgameStatus side effects (revision + message);
// pass nil only in tests that don't exercise status mutation.
func NewAdminHandler(adminRepo *repository.AdminRepository, adminSvc *service.AdminService, hook *search.Hook) *AdminHandler {
	return &AdminHandler{adminRepo: adminRepo, adminSvc: adminSvc, searchHook: hook}
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

// UpdateGalgameStatus changes a galgame's status and writes the matching
// revision + (when relevant) a galgame_message row in one transaction.
//
// Allowed target statuses: 0 (publish/approve), 1 (ban), 4 (decline).
// 4 is only valid from source status=3. See admin_dto.go for rationale.
func (h *AdminHandler) UpdateGalgameStatus(c fiber.Ctx) error {
	adminUserID, _ := c.Locals("user_id").(uint)
	if adminUserID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.AdminUpdateGalgameStatusRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if h.adminSvc == nil {
		// Defensive: admin service must be wired up in production.
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if err := h.adminSvc.UpdateStatus(c.Context(), int(adminUserID), id, req.Status, req.Reason); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			switch appErr.Code {
			case errors.ErrGalgameNotFound:
				return response.NotFound(c, appErr.Code)
			default:
				return response.BadRequest(c, appErr.Code)
			}
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	if h.searchHook != nil {
		h.searchHook.Galgame(id)
	}
	return response.Success(c, fiber.Map{"id": id, "status": req.Status})
}
