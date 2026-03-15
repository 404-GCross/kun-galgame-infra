package handler

import (
	"time"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/service"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

const refreshTokenCookieName = "refresh_token"

// AuthHandler handles authentication requests
type AuthHandler struct {
	authService *service.AuthService
	cfg         *config.Config
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(authService *service.AuthService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{authService: authService, cfg: cfg}
}

// setRefreshTokenCookie sets the refresh token as an httpOnly cookie
func (h *AuthHandler) setRefreshTokenCookie(c fiber.Ctx, token string) {
	secure := h.cfg.Server.Env == "production"
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    token,
		Path:     "/api/v1/auth",
		HTTPOnly: true,
		Secure:   secure,
		SameSite: fiber.CookieSameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()), // 7 days
	})
}

// clearRefreshTokenCookie clears the refresh token cookie
func (h *AuthHandler) clearRefreshTokenCookie(c fiber.Ctx) {
	secure := h.cfg.Server.Env == "production"
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		HTTPOnly: true,
		Secure:   secure,
		SameSite: fiber.CookieSameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Register handles user registration
func (h *AuthHandler) Register(c fiber.Ctx) error {
	var req dto.RegisterRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	user, err := h.authService.Register(c.Context(), &req)
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
		Roles:       []string{}, // New user has no roles yet
		CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// Login handles user login
func (h *AuthHandler) Login(c fiber.Ctx) error {
	var req dto.LoginRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Set user agent and IP
	req.UserAgent = string(c.Request().Header.UserAgent())
	req.IPAddress = c.IP()

	tokens, user, err := h.authService.Login(c.Context(), &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.Unauthorized(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Set refresh token as httpOnly cookie
	h.setRefreshTokenCookie(c, tokens.RefreshToken)

	return response.Success(c, dto.LoginResponse{
		User: dto.UserResponse{
			UUID:        user.UUID,
			Name:        user.Name,
			Email:       user.Email,
			Avatar:      user.Avatar,
			Bio:         user.Bio,
			Moemoepoint: user.Moemoepoint,
			Status:      user.Status,
			Roles:       user.RoleNames(),
			CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
		},
		AccessToken: tokens.AccessToken,
	})
}

// Logout handles user logout
func (h *AuthHandler) Logout(c fiber.Ctx) error {
	// Get token from header
	token := c.Get("Authorization")
	if token != "" {
		// Remove "Bearer " prefix if present
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		_ = h.authService.Logout(c.Context(), token)
	}

	// Clear refresh token cookie
	h.clearRefreshTokenCookie(c)

	return response.Success(c, nil)
}

// Refresh handles token refresh
func (h *AuthHandler) Refresh(c fiber.Ctx) error {
	// Read refresh token from httpOnly cookie first, then fallback to body
	rt := c.Cookies(refreshTokenCookieName)
	if rt == "" {
		var req dto.RefreshRequest
		if err := c.Bind().JSON(&req); err == nil && req.RefreshToken != "" {
			rt = req.RefreshToken
		}
	}
	if rt == "" {
		return response.Unauthorized(c, errors.ErrAuthInvalidToken)
	}

	tokens, err := h.authService.RefreshToken(c.Context(), rt)
	if err != nil {
		// Clear invalid cookie
		h.clearRefreshTokenCookie(c)
		if appErr, ok := err.(*errors.AppError); ok {
			return response.Unauthorized(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Set new refresh token as httpOnly cookie
	h.setRefreshTokenCookie(c, tokens.RefreshToken)

	// Only return access token in body
	return response.Success(c, dto.RefreshResponse{
		AccessToken: tokens.AccessToken,
	})
}

// Me returns the current user
func (h *AuthHandler) Me(c fiber.Ctx) error {
	userUUID := c.Locals("user_uuid").(string)

	user, err := h.authService.GetCurrentUserWithRoles(c.Context(), userUUID)
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
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
		CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// ChangePassword handles password change
func (h *AuthHandler) ChangePassword(c fiber.Ctx) error {
	var req dto.ChangePasswordRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	userUUID := c.Locals("user_uuid").(string)

	if err := h.authService.ChangePassword(c.Context(), userUUID, req.OldPassword, req.NewPassword); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "密码修改成功", nil)
}

// ForgotPassword handles forgot password request
func (h *AuthHandler) ForgotPassword(c fiber.Ctx) error {
	var req dto.ForgotPasswordRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Call service - ignore errors for security (don't reveal if email exists)
	_ = h.authService.ForgotPassword(c.Context(), req.Email)

	// For security, always return success even if email doesn't exist
	return response.SuccessWithMessage(c, "如果该邮箱已注册，我们已发送密码重置链接", nil)
}

// ResetPassword handles password reset
func (h *AuthHandler) ResetPassword(c fiber.Ctx) error {
	var req dto.ResetPasswordRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if err := h.authService.ResetPassword(c.Context(), req.Token, req.Password); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.BadRequest(c, errors.ErrAuthCodeInvalid)
	}

	return response.SuccessWithMessage(c, "密码重置成功", nil)
}

// GetProfile gets a user profile by UUID
func (h *AuthHandler) GetProfile(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	user, err := h.authService.GetCurrentUser(c.Context(), uuid)
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
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
		CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}
