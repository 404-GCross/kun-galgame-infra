package handler

import (
	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// AdminHandler handles admin requests
type AdminHandler struct {
	adminService *service.AdminService
}

// NewAdminHandler creates a new AdminHandler
func NewAdminHandler(adminService *service.AdminService) *AdminHandler {
	return &AdminHandler{adminService: adminService}
}

// ListUsers lists all users with pagination
func (h *AdminHandler) ListUsers(c fiber.Ctx) error {
	var req dto.UserListRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	result, err := h.adminService.ListUsers(c.Context(), &req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, result)
}

// GetUser gets a user by UUID
func (h *AdminHandler) GetUser(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	user, err := h.adminService.GetUser(c.Context(), uuid)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, user)
}

// UpdateUser updates a user
func (h *AdminHandler) UpdateUser(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	var req dto.UpdateUserRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	user, err := h.adminService.UpdateUser(c.Context(), uuid, &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, dto.UserResponse{
		UUID:        user.UUID,
		Name:        user.Name,
		Email:       user.Email,
		Avatar:      user.Avatar,
		Bio:         user.Bio,
		Moemoepoint: user.Moemoepoint,
		Status:      user.Status,
		Roles:       user.RoleNames(),
		CreatedAt:   user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// BanUser bans a user
func (h *AdminHandler) BanUser(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	if err := h.adminService.BanUser(c.Context(), uuid); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "用户已封禁", nil)
}

// UnbanUser unbans a user
func (h *AdminHandler) UnbanUser(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	if err := h.adminService.UnbanUser(c.Context(), uuid); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "用户已解封", nil)
}

// DeleteUserSessions deletes all sessions for a user (force logout)
func (h *AdminHandler) DeleteUserSessions(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	if err := h.adminService.DeleteUserSessions(c.Context(), uuid); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "用户会话已清除", nil)
}
