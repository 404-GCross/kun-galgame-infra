package handler

import (
	"slices"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/service"
	sitePerm "api/internal/platform/site/perm"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// manageableRoles are the roles assignable/revocable via the admin API. `ren`
// is deliberately absent — it is DB-provisioned only, never granted through
// the API.
var manageableRoles = []string{"user", "creator", "moderator", "admin"}

// callerCanManageRole reports whether the authenticated caller may grant or
// revoke `role` on another user. The target-side guard (manageableRoles: `ren`
// is never API-manageable) is unchanged; the caller-side ability is now
// permission-first:
//   - moderator / creator targets → oauth.roles.grant_basic (admin ∪ ren)
//   - admin target, and the implicit `user` base → oauth.roles.grant_admin (ren)
//
// This preserves the prior matrix exactly (ren manages all manageable roles;
// admin manages only moderator/creator).
func callerCanManageRole(c fiber.Ctx, role string) bool {
	if !slices.Contains(manageableRoles, role) {
		return false
	}
	roles := callerRoles(c)
	if role == "admin" || role == "user" {
		return sitePerm.Resolver.Can(roles, sitePerm.RolesGrantAdmin)
	}
	return sitePerm.Resolver.Can(roles, sitePerm.RolesGrantBasic)
}

// callerRoles reads the authenticated caller's OAuth roles from the Fiber
// locals populated by middleware.Auth.
func callerRoles(c fiber.Ctx) []string {
	roles, _ := c.Locals("user_roles").([]string)
	return roles
}

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
	// Enforce the DTO's sort_by oneof (and page/limit bounds): without this
	// the validation tags never run (no app-wide StructValidator), letting an
	// arbitrary sort_by reach the query layer.
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	result, err := h.adminService.ListUsers(c.Context(), &req, sitePerm.Resolver.Can(callerRoles(c), sitePerm.UsersPIIView))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, result)
}

// RegistrationStats returns the daily user-registration series (last `days`
// days, 1-90, default 30) plus summary figures. GET /admin/stats/registrations.
func (h *AdminHandler) RegistrationStats(c fiber.Ctx) error {
	var req dto.RegistrationStatsRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	days := req.Days
	if days == 0 {
		days = 30
	}

	result, err := h.adminService.RegistrationStats(c.Context(), days)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, result)
}

// HourlyRegistrationStats returns the 24-hour registration series for a single
// day. GET /admin/stats/registrations/hourly?date=YYYY-MM-DD.
func (h *AdminHandler) HourlyRegistrationStats(c fiber.Ctx) error {
	var req dto.HourlyStatsRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	result, err := h.adminService.HourlyRegistrations(c.Context(), req.Date)
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

	user, err := h.adminService.GetUser(c.Context(), uuid, sitePerm.Resolver.Can(callerRoles(c), sitePerm.UsersPIIView))
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			if appErr.Code == errors.ErrForbidden {
				return response.Forbidden(c, appErr.Code)
			}
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, user)
}

// AssignRole grants a role to a user. POST /admin/users/:uuid/roles {role}.
// Authorization: ren may grant any manageable role; admin may grant moderator
// only (callerCanManageRole). `ren` itself is not grantable (DTO oneof).
func (h *AdminHandler) AssignRole(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}
	// Managing your own roles is almost always a mistake (e.g. ren revoking
	// their own admin would lock them out of /admin), so refuse self-edits.
	if callerUUID, _ := c.Locals("user_uuid").(string); callerUUID == uuid {
		return response.ForbiddenMsg(c, errors.ErrForbidden, "不能修改自己的角色")
	}

	var req dto.AssignRoleRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	if !callerCanManageRole(c, req.Role) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, "无权赋予该角色")
	}

	roles, err := h.adminService.AssignRole(c.Context(), uuid, req.Role)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"roles": roles})
}

// RevokeRole removes a role from a user. DELETE /admin/users/:uuid/roles/:role.
// Same authorization matrix as AssignRole.
func (h *AdminHandler) RevokeRole(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	role := c.Params("role")
	if uuid == "" || role == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}
	if callerUUID, _ := c.Locals("user_uuid").(string); callerUUID == uuid {
		return response.ForbiddenMsg(c, errors.ErrForbidden, "不能修改自己的角色")
	}
	if !callerCanManageRole(c, role) {
		return response.ForbiddenMsg(c, errors.ErrForbidden, "无权移除该角色")
	}

	roles, err := h.adminService.RevokeRole(c.Context(), uuid, role)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"roles": roles})
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
			// Not-found → 404 (matches GetUser/BanUser/…); banning a peer
			// admin → 403 (matches BanUser); name/email conflicts stay 400.
			if appErr.Code == errors.ErrForbidden {
				return response.Forbidden(c, appErr.Code)
			}
			if appErr.Code == errors.ErrAuthUserNotFound {
				return response.NotFound(c, appErr.Code)
			}
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, dto.UserResponse{
		UUID:            user.UUID,
		Name:            user.Name,
		Email:           user.Email,
		Avatar:          user.Avatar,
		AvatarImageHash: user.AvatarImageHash,
		Bio:             user.Bio,
		Moemoepoint:     user.Moemoepoint,
		Status:          user.Status,
		IsAnonymized:    user.IsAnonymized(),
		Roles:           user.RoleNames(),
		CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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
			if appErr.Code == errors.ErrForbidden {
				return response.Forbidden(c, appErr.Code)
			}
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
			if appErr.Code == errors.ErrForbidden {
				return response.Forbidden(c, appErr.Code)
			}
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "用户已解封", nil)
}

// AnonymizeUser scrubs a user's PII and locks the account (irreversible).
// For severe spam / PII abuse. Keeps the row so cross-service FKs survive.
func (h *AdminHandler) AnonymizeUser(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	if err := h.adminService.AnonymizeUser(c.Context(), uuid); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			if appErr.Code == errors.ErrForbidden {
				return response.Forbidden(c, appErr.Code)
			}
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "用户已注销并匿名化", nil)
}

// DeleteUserSessions deletes all sessions for a user (force logout)
func (h *AdminHandler) DeleteUserSessions(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	if err := h.adminService.DeleteUserSessions(c.Context(), uuid); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			if appErr.Code == errors.ErrForbidden {
				return response.Forbidden(c, appErr.Code)
			}
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "用户会话已清除", nil)
}
