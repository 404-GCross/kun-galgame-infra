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
	"github.com/google/uuid"
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

// clearBrowserCookie clears the kg_browser session-bag anchor (logout all).
func (h *AuthHandler) clearBrowserCookie(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     browserCookieName,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		Secure:   h.cfg.Server.Env == "production",
		SameSite: fiber.CookieSameSiteLaxMode,
		MaxAge:   -1,
	})
}

// browserCookieName anchors the multi-account "session bag" (docs/auth/02): all
// OP login sessions sharing this opaque id belong to the same browser.
const browserCookieName = "kg_browser"

// browserID returns this browser's session-bag id, reading the kg_browser
// cookie or minting + setting a new one (httpOnly, long-lived, host-only on the
// OP). Behaviour-preserving for single-account — the bag is simply size 1.
func (h *AuthHandler) browserID(c fiber.Ctx) string {
	id := c.Cookies(browserCookieName)
	if id == "" {
		id = uuid.NewString()
	}
	// Always (re)set so the anchor's 1-year TTL slides forward on every login and
	// switch — a bag kept alive purely by switching never expires mid-use.
	c.Cookie(&fiber.Cookie{
		Name:     browserCookieName,
		Value:    id,
		Path:     "/",
		HTTPOnly: true,
		Secure:   h.cfg.Server.Env == "production",
		SameSite: fiber.CookieSameSiteLaxMode,
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
	})
	return id
}

// ListSessions serves GET /auth/sessions — the accounts in this browser's
// session bag, for the same-site account chooser (docs/integration/oauth/09
// §3.3). Reads the kg_browser anchor + the refresh_token (to flag the active
// account). Cross-TLD apps use the redirect chooser instead.
func (h *AuthHandler) ListSessions(c fiber.Ctx) error {
	callerUUID := c.Locals("user_uuid").(string)
	browserID := c.Cookies(browserCookieName)
	activeRT := c.Cookies(refreshTokenCookieName)
	items, err := h.authService.ListBrowserSessions(c.Context(), browserID, callerUUID, activeRT)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.Unauthorized(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, dto.ListSessionsResponse{Items: items})
}

// SwitchSession serves POST /auth/sessions/switch {sub} — switch the active
// account to another account already in this browser's bag (no re-auth, except
// admin/ren → 10016 step-up-required, which the caller resolves via prompt=login).
// See docs/integration/oauth/09 §3.6.
func (h *AuthHandler) SwitchSession(c fiber.Ctx) error {
	var req dto.AccountSubRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	callerUUID := c.Locals("user_uuid").(string)
	browserID := c.Cookies(browserCookieName)
	tokens, user, err := h.authService.SwitchActiveSession(c.Context(), browserID, callerUUID, req.Sub)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.Unauthorized(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	h.setRefreshTokenCookie(c, tokens.RefreshToken)
	h.browserID(c) // re-anchor: slide the bag cookie's TTL forward on switch-only activity
	return response.Success(c, dto.LoginResponse{
		User: dto.UserResponse{
			UUID:            user.UUID,
			Name:            user.Name,
			Email:           user.Email,
			Avatar:          user.Avatar,
			AvatarImageHash: user.AvatarImageHash,
			Bio:             user.Bio,
			Moemoepoint:     user.Moemoepoint,
			Status:          user.Status,
			Roles:           user.RoleNames(),
			CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
		AccessToken: tokens.AccessToken,
	})
}

// LogoutAccount serves POST /auth/sessions/logout {sub} — remove one account
// from this browser's bag. If it was the active account, the refresh cookie is
// cleared (the caller reconciles / picks another). See oauth doc 09 §3.3.
func (h *AuthHandler) LogoutAccount(c fiber.Ctx) error {
	var req dto.AccountSubRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	callerUUID := c.Locals("user_uuid").(string)
	browserID := c.Cookies(browserCookieName)
	activeRT := c.Cookies(refreshTokenCookieName)
	removed, clearedActive, err := h.authService.LogoutBrowserAccount(c.Context(), browserID, callerUUID, req.Sub, activeRT)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.Unauthorized(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if !removed {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}
	if clearedActive {
		h.clearRefreshTokenCookie(c)
	}
	return response.Success(c, nil)
}

// LogoutAll serves POST /auth/sessions/logout-all — remove every account from
// this browser's bag + clear the anchor. See oauth doc 09 §3.3.
func (h *AuthHandler) LogoutAll(c fiber.Ctx) error {
	browserID := c.Cookies(browserCookieName)
	if err := h.authService.LogoutBrowserAll(c.Context(), browserID); err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	h.clearRefreshTokenCookie(c)
	h.clearBrowserCookie(c)
	return response.Success(c, nil)
}

// SendRegisterCode handles `POST /auth/register/send-code` — the first
// half of the two-step unified registration flow. The follow-up call is
// `POST /auth/register` with the 6-digit code in the body. See
// docs/integration/oauth/05-registration.md.
func (h *AuthHandler) SendRegisterCode(c fiber.Ctx) error {
	var req dto.SendRegisterCodeRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if err := h.authService.SendRegisterCode(c.Context(), req.Name, req.Email); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "验证码已发送到该邮箱", nil)
}

// Register handles user registration. Registration is "register + login":
// the service issues a token pair and creates a session row, the handler
// drops the refresh_token into an httpOnly cookie and returns the access
// token + user in the same response shape as Login. Without this, the
// unified-registration redirect chain documented in
// docs/integration/oauth/05-registration.md cannot work — `/auth/register`
// has to leave the user authenticated so the chained `/oauth/authorize`
// hop can issue a code immediately.
func (h *AuthHandler) Register(c fiber.Ctx) error {
	var req dto.RegisterRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Capture session context — same pattern as Login.
	req.UserAgent = string(c.Request().Header.UserAgent())
	req.IPAddress = c.IP()
	req.BrowserID = h.browserID(c)

	tokens, user, err := h.authService.Register(c.Context(), &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.setRefreshTokenCookie(c, tokens.RefreshToken)

	return response.Success(c, dto.LoginResponse{
		User: dto.UserResponse{
			UUID:            user.UUID,
			Name:            user.Name,
			Email:           user.Email,
			Avatar:          user.Avatar,
			AvatarImageHash: user.AvatarImageHash,
			Bio:             user.Bio,
			Moemoepoint:     user.Moemoepoint,
			Status:          user.Status,
			Roles:           []string{}, // New user has no roles yet
			CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		},
		AccessToken: tokens.AccessToken,
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
	req.BrowserID = h.browserID(c)

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
			UUID:            user.UUID,
			Name:            user.Name,
			Email:           user.Email,
			Avatar:          user.Avatar,
			AvatarImageHash: user.AvatarImageHash,
			Bio:             user.Bio,
			Moemoepoint:     user.Moemoepoint,
			Status:          user.Status,
			Roles:           user.RoleNames(),
			CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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

// visibleEmail gates the address on the token's OIDC `email` scope — the same
// filter /oauth/userinfo applies, via the one shared rule (service.ScopeGrants,
// which also decides that a scope-less first-party password-login token sees
// everything). Without this gate a client granted only `openid profile` could
// read here the address userinfo deliberately withholds, which made that filter
// theatre.
//
// Denied means present-and-empty, not omitted: `email` on UserResponse carries
// no omitempty and existing consumers read it as a string. Contract:
// docs/integration/oauth/02-user-profile.md.
func visibleEmail(scope, email string) string {
	if service.ScopeGrants(scope, "email") {
		return email
	}
	return ""
}

// Me returns the current user
func (h *AuthHandler) Me(c fiber.Ctx) error {
	userUUID := c.Locals("user_uuid").(string)
	scope, _ := c.Locals("user_scope").(string)

	user, err := h.authService.GetCurrentUserWithRoles(c.Context(), userUUID)
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}

	return response.Success(c, dto.UserResponse{
		UUID:            user.UUID,
		Name:            user.Name,
		Email:           visibleEmail(scope, user.Email),
		Avatar:          user.Avatar,
		AvatarImageHash: user.AvatarImageHash,
		Bio:             user.Bio,
		Moemoepoint:     user.Moemoepoint,
		Status:          user.Status,
		Roles:           user.RoleNames(),
		CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// UpdateProfile handles partial profile updates for the authenticated
// user. Body fields are all optional; nil = leave alone, non-nil = set.
//
// Returns the updated UserResponse so the client doesn't need a separate
// /auth/me round-trip after the mutation.
func (h *AuthHandler) UpdateProfile(c fiber.Ctx) error {
	var req dto.UpdateProfileRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	userUUID := c.Locals("user_uuid").(string)
	scope, _ := c.Locals("user_scope").(string)

	user, err := h.authService.UpdateProfile(c.Context(), userUUID, &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	roles := make([]string, 0, len(user.Roles))
	for _, r := range user.Roles {
		roles = append(roles, r.Name)
	}

	return response.Success(c, dto.UserResponse{
		UUID:            user.UUID,
		Name:            user.Name,
		Email:           visibleEmail(scope, user.Email),
		Avatar:          user.Avatar,
		AvatarImageHash: user.AvatarImageHash,
		Bio:             user.Bio,
		Moemoepoint:     user.Moemoepoint,
		Status:          user.Status,
		Roles:           roles,
		CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
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
			// Genuine invalid/expired/used token → 400 (ErrAuthInvalidToken).
			return response.BadRequest(c, appErr.Code)
		}
		// Real infra/DB failure (hashing, UpdatePassword, MarkAsUsed) — a 500,
		// not a misleading "验证码无效" that makes the user retry a valid token.
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "密码重置成功", nil)
}

// GetProfile gets a PUBLIC user profile by UUID. Any authenticated user can
// fetch any uuid, so this deliberately omits PII (email) and account state
// (status) — only public-facing fields are returned. Use /auth/me for the
// caller's own full profile. Roles are preloaded so they are not silently
// empty (GetCurrentUserWithRoles, not GetCurrentUser).
func (h *AuthHandler) GetProfile(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrMissingParam)
	}

	user, err := h.authService.GetCurrentUserWithRoles(c.Context(), uuid)
	if err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}

	return response.Success(c, dto.UserResponse{
		UUID:            user.UUID,
		Name:            user.Name,
		Avatar:          user.Avatar,
		AvatarImageHash: user.AvatarImageHash,
		Bio:             user.Bio,
		Moemoepoint:     user.Moemoepoint,
		Roles:           user.RoleNames(),
		CreatedAt:       user.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// SendEmailChangeCode sends a verification code to the user's current email
func (h *AuthHandler) SendEmailChangeCode(c fiber.Ctx) error {
	var req dto.SendEmailChangeCodeRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	userUUID := c.Locals("user_uuid").(string)

	if err := h.authService.SendEmailChangeCode(c.Context(), userUUID, req.NewEmail); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "验证码已发送到当前邮箱", nil)
}

// ChangeEmail changes the user's email after verifying the code
func (h *AuthHandler) ChangeEmail(c fiber.Ctx) error {
	var req dto.ChangeEmailRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	userUUID := c.Locals("user_uuid").(string)

	if err := h.authService.ChangeEmail(c.Context(), userUUID, req.Code, req.NewEmail); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.SuccessWithMessage(c, "邮箱修改成功", nil)
}
