package handler

import (
	"net/url"

	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/service"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// buildRedirectURL safely constructs a redirect URL with query parameters
func buildRedirectURL(baseURI, code, state string) (string, error) {
	u, err := url.Parse(baseURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// OAuthHandler handles OAuth 2.0 requests
type OAuthHandler struct {
	oauthService *service.OAuthService
	cfg          *config.Config
}

// NewOAuthHandler creates a new OAuthHandler
func NewOAuthHandler(oauthService *service.OAuthService, cfg *config.Config) *OAuthHandler {
	return &OAuthHandler{oauthService: oauthService, cfg: cfg}
}

// Authorize handles the OAuth authorization request.
// If the user is not logged in, redirects to the login page.
// If logged in, redirects to the frontend consent page so the user can approve.
func (h *OAuthHandler) Authorize(c fiber.Ctx) error {
	var req dto.AuthorizeRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Validate client and redirect URI
	_, err := h.oauthService.ValidateClient(c.Context(), req.ClientID, req.RedirectURI)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	frontendURL := h.cfg.Server.FrontendURL
	fullURI := string(c.Request().URI().FullURI())
	returnURL := url.QueryEscape(fullURI)

	// Check if user is authenticated (set by OptionalAuth middleware)
	userUUIDRaw := c.Locals("user_uuid")
	if userUUIDRaw == nil || userUUIDRaw.(string) == "" {
		// Not logged in — redirect to login page with return URL
		loginURL := frontendURL + "/auth/login?redirect=" + returnURL
		return c.Redirect().To(loginURL)
	}

	// Logged in — redirect to consent page so user can approve
	consentURL := frontendURL + "/oauth/consent?redirect=" + returnURL
	return c.Redirect().To(consentURL)
}

// Consent handles the user's authorization consent (POST).
// Called after the user approves on the frontend consent page.
func (h *OAuthHandler) Consent(c fiber.Ctx) error {
	var req dto.AuthorizeRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Validate client and redirect URI
	_, err := h.oauthService.ValidateClient(c.Context(), req.ClientID, req.RedirectURI)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	userUUID := c.Locals("user_uuid").(string)
	userID, err := h.oauthService.GetUserIDByUUID(c.Context(), userUUID)
	if err != nil {
		return response.Unauthorized(c, errors.ErrAuthUserNotFound)
	}

	// Create authorization code
	codeChallengeMethod := req.CodeChallengeMethod
	if codeChallengeMethod == "" && req.CodeChallenge != "" {
		codeChallengeMethod = "S256" // Default to S256
	}

	code, err := h.oauthService.CreateAuthorizationCode(
		c.Context(),
		userID,
		req.ClientID,
		req.RedirectURI,
		req.Scope,
		req.CodeChallenge,
		codeChallengeMethod,
	)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Build redirect URL safely with url.URL
	redirectURL, err := buildRedirectURL(req.RedirectURI, code, req.State)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"redirect_url": redirectURL,
	})
}

// Token handles the OAuth token exchange request.
// Supports grant_type=authorization_code and grant_type=refresh_token.
func (h *OAuthHandler) Token(c fiber.Ctx) error {
	var req dto.TokenRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	switch req.GrantType {
	case "authorization_code":
		tokenResp, err := h.oauthService.ExchangeCode(c.Context(), &req)
		if err != nil {
			if appErr, ok := err.(*errors.AppError); ok {
				return response.BadRequest(c, appErr.Code)
			}
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		return c.JSON(tokenResp)

	case "refresh_token":
		if req.RefreshToken == "" {
			return response.BadRequest(c, errors.ErrBadRequest)
		}
		tokenResp, err := h.oauthService.RefreshWithClient(c.Context(), req.RefreshToken, req.ClientID, req.ClientSecret)
		if err != nil {
			if appErr, ok := err.(*errors.AppError); ok {
				return response.Unauthorized(c, appErr.Code)
			}
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		return c.JSON(tokenResp)

	default:
		return response.BadRequest(c, errors.ErrOAuthInvalidGrant)
	}
}

// UserInfo returns user information for the authenticated user.
func (h *OAuthHandler) UserInfo(c fiber.Ctx) error {
	userUUID := c.Locals("user_uuid").(string)

	info, err := h.oauthService.GetUserInfo(c.Context(), userUUID)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return c.JSON(info)
}

// Revoke revokes a refresh token.
func (h *OAuthHandler) Revoke(c fiber.Ctx) error {
	var req struct {
		Token string `json:"token" validate:"required"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if req.Token == "" {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	_ = h.oauthService.RevokeToken(c.Context(), req.Token)

	// RFC 7009: always return 200 OK regardless of whether the token was found
	return response.Success(c, nil)
}
