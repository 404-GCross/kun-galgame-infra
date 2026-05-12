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
// Validates the client, then always redirects to the frontend /oauth/authorize page.
// The frontend handles login detection and consent UI, then calls POST /oauth/authorize/consent.
// This avoids infinite redirect loops caused by the API not having access to the frontend's auth state.
func (h *OAuthHandler) Authorize(c fiber.Ctx) error {
	var req dto.AuthorizeRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Validate client and redirect URI before redirecting
	_, err := h.oauthService.ValidateClient(c.Context(), req.ClientID, req.RedirectURI)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Pass all OAuth params to the frontend, which handles login + consent
	q := url.Values{}
	q.Set("client_id", req.ClientID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("response_type", req.ResponseType)
	q.Set("scope", req.Scope)
	q.Set("state", req.State)
	if req.CodeChallenge != "" {
		q.Set("code_challenge", req.CodeChallenge)
	}
	if req.CodeChallengeMethod != "" {
		q.Set("code_challenge_method", req.CodeChallengeMethod)
	}

	consentURL := h.cfg.Server.FrontendURL + "/oauth/authorize?" + q.Encode()
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
		// Domain errors (e.g. ErrOAuthInvalidScope when the requested
		// scope is not in the client's allow-list) must surface as 4xx
		// with the actual code — wrapping everything as 500 hid these
		// from clients and made misconfigured-scope requests look like
		// a server outage.
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
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
		return response.Success(c, tokenResp)

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
		return response.Success(c, tokenResp)

	default:
		return response.BadRequest(c, errors.ErrOAuthInvalidGrant)
	}
}

// UserInfo returns user information for the authenticated user.
// Fields are filtered based on the scope embedded in the access token.
func (h *OAuthHandler) UserInfo(c fiber.Ctx) error {
	userUUID := c.Locals("user_uuid").(string)
	scope, _ := c.Locals("user_scope").(string)

	info, err := h.oauthService.GetUserInfo(c.Context(), userUUID, scope)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, info)
}

// Revoke revokes an OAuth token (RFC 7009).
//
// Accepts either an access_token or a refresh_token in the `token`
// field. Per RFC 7009 §2.1 servers MUST handle both token types — a
// client should be able to revoke an access_token even if it lost its
// refresh_token. We use the optional `token_type_hint` to short-circuit
// when the caller knows the type, otherwise we try refresh_token first
// then access_token.
//
// Always returns 200 — never disclose whether the token existed.
func (h *OAuthHandler) Revoke(c fiber.Ctx) error {
	var req struct {
		Token         string `json:"token" validate:"required"`
		TokenTypeHint string `json:"token_type_hint"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if req.Token == "" {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	_ = h.oauthService.RevokeToken(c.Context(), req.Token, req.TokenTypeHint)

	// RFC 7009: always return 200 OK regardless of whether the token was found
	return response.Success(c, nil)
}
