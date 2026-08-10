package handler

import (
	"strconv"

	"api/internal/platform/auth/dto"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

func (h *AdminHandler) AssignSiteRole(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}
	if callerUUID, _ := c.Locals("user_uuid").(string); callerUUID == uuid {
		return response.ForbiddenMsg(c, errors.ErrForbidden, "不能修改自己的角色")
	}

	var req dto.AssignSiteRoleRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	grantedBy, _ := c.Locals("user_id").(uint)
	if err := h.adminService.AssignSiteRole(c.Context(), uuid, grantedBy, &req); err != nil {
		return siteRoleError(c, err)
	}
	return response.Success(c, fiber.Map{"granted": true})
}

func (h *AdminHandler) RevokeSiteRole(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}
	if callerUUID, _ := c.Locals("user_uuid").(string); callerUUID == uuid {
		return response.ForbiddenMsg(c, errors.ErrForbidden, "不能修改自己的角色")
	}

	siteID64, err := strconv.ParseUint(c.Query("site_id"), 10, 64)
	roleName := c.Query("role_name")
	if err != nil || siteID64 == 0 || roleName == "" {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "site_id and role_name are required")
	}

	if err := h.adminService.RevokeSiteRole(c.Context(), uuid, uint(siteID64), roleName); err != nil {
		return siteRoleError(c, err)
	}
	return response.Success(c, fiber.Map{"revoked": true})
}

func siteRoleError(c fiber.Ctx, err error) error {
	if appErr, ok := err.(*errors.AppError); ok {
		switch appErr.Code {
		case errors.ErrValidationFailed:
			return response.BadRequestMsg(c, appErr.Code, appErr.Message)
		case errors.ErrSiteNotFound, errors.ErrAuthUserNotFound:
			return response.NotFound(c, appErr.Code)
		}
	}
	return response.InternalError(c, errors.ErrOperationFailed)
}
