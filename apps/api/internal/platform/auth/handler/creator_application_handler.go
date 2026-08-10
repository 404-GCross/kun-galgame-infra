package handler

import (
	"strconv"

	"api/internal/platform/auth/model"
	"api/internal/platform/auth/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"gorm.io/datatypes"
)

type CreatorApplicationHandler struct {
	svc *service.CreatorApplicationService
}

func NewCreatorApplicationHandler(svc *service.CreatorApplicationService) *CreatorApplicationHandler {
	return &CreatorApplicationHandler{svc: svc}
}

type applyCreatorRequest struct {
	Source   string         `json:"source" validate:"required,oneof=forum moyu"`
	Message  string         `json:"message" validate:"omitempty,max=1000"`
	Evidence datatypes.JSON `json:"evidence"`
}

func (h *CreatorApplicationHandler) Apply(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	var req applyCreatorRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	app, err := h.svc.Apply(c.Context(), userID, req.Source, req.Message, req.Evidence)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, app)
}

func (h *CreatorApplicationHandler) MyApplication(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	app, err := h.svc.MyApplication(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, app)
}

func (h *CreatorApplicationHandler) AdminList(c fiber.Ctx) error {
	status := c.Query("status", model.CreatorAppPending)
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	items, total, err := h.svc.List(c.Context(), status, page, limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"items": items, "total": total})
}

func (h *CreatorApplicationHandler) AdminApprove(c fiber.Ctx) error {
	reviewerID, _ := c.Locals("user_id").(uint)
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := h.svc.Approve(c.Context(), uint(id), reviewerID); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"id": id, "status": model.CreatorAppApproved})
}

type declineCreatorRequest struct {
	Reason string `json:"reason" validate:"omitempty,max=500"`
}

func (h *CreatorApplicationHandler) AdminDecline(c fiber.Ctx) error {
	reviewerID, _ := c.Locals("user_id").(uint)
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	var req declineCreatorRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	if err := h.svc.Decline(c.Context(), uint(id), reviewerID, req.Reason); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"id": id, "status": model.CreatorAppDeclined})
}
