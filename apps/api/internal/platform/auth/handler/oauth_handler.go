package handler

import (
	"encoding/base64"
	"log/slog"
	"net/url"
	"strings"

	"api/internal/middleware"
	"api/internal/platform/auth/dto"
	"api/internal/platform/auth/service"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

// bindByContentType binds an x-www-form-urlencoded body (RFC 6749 — what
// standard OAuth/OIDC libraries send to the token/revoke endpoints) or a JSON
// body (legacy first-party clients), chosen by Content-Type.
func bindByContentType(c fiber.Ctx, out any) error {
	if strings.HasPrefix(c.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return c.Bind().Form(out)
	}
	return c.Bind().JSON(out)
}

// basicClientAuth extracts client_id + client_secret from an HTTP Basic
// Authorization header (RFC 6749 §2.3.1 client_secret_basic). The two values
// are form-urlencoded before base64 per the spec, so they're unescaped here.
func basicClientAuth(authHeader string) (clientID, secret string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, prefix))
	if err != nil {
		return "", "", false
	}
	id, sec, found := strings.Cut(string(raw), ":")
	if !found {
		return "", "", false
	}
	if u, err := url.QueryUnescape(id); err == nil {
		id = u
	}
	if u, err := url.QueryUnescape(sec); err == nil {
		sec = u
	}
	return id, sec, true
}

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

// buildErrorRedirectURL appends an OAuth/OIDC error (RFC 6749 §4.1.2.1) to the
// RP's redirect_uri — for user-denied consent and prompt=none interaction
// errors. The caller MUST have validated redirect_uri against the client first.
func buildErrorRedirectURL(baseURI, errCode, state string) (string, error) {
	u, err := url.Parse(baseURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("error", errCode)
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

// --- OAuth/OIDC protocol wire format ---
//
// The endpoints below (/oauth/token, /oauth/userinfo, /oauth/revoke) belong to
// RFC 6749 / RFC 6750 / RFC 7009 and OIDC Core, not to this codebase's house
// API style. They therefore answer in the spec's own shapes — bare top-level
// JSON on success, {error, error_description} on failure — with no
// {code,message,data} envelope and no switch to turn one on. The house
// envelope remains correct everywhere else (/auth/me, /users/*, ...); the line
// is drawn at the protocol boundary, not at the repo boundary.

// oauthErrString maps an internal AppError code to an RFC 6749 §5.2 error code.
func oauthErrString(appCode int) string {
	switch appCode {
	case errors.ErrOAuthInvalidClient, errors.ErrOAuthInvalidClientSecret:
		return "invalid_client"
	case errors.ErrOAuthInvalidCode, errors.ErrOAuthInvalidCodeVerifier, errors.ErrOAuthInvalidRedirectURI,
		// Auth-session-dead errors on the refresh path map to invalid_grant so
		// RPs treat them as "refresh dead → re-login" (not a retryable request).
		// A banned user is re-blocked at the login they're bounced to.
		errors.ErrAuthUserBanned, errors.ErrAuthTokenExpired, errors.ErrAuthInvalidToken:
		return "invalid_grant"
	case errors.ErrOAuthInvalidScope:
		return "invalid_scope"
	case errors.ErrOAuthInvalidGrant:
		// Grant-allowlist rejection (RFC 6749 §5.2 unauthorized_client) —
		// refresh-dead for RPs, same as the legacy numeric 15005.
		return "unauthorized_client"
	case errors.ErrOAuthUnsupportedGrantType:
		return "unsupported_grant_type"
	default:
		return "invalid_request"
	}
}

// protoErr writes a protocol-endpoint error as an RFC 6749 §5.2 error object.
// invalid_client is 401 per §5.2; everything else is 400.
func protoErr(c fiber.Ctx, appCode int) error {
	errStr := oauthErrString(appCode)
	status := fiber.StatusBadRequest
	if errStr == "invalid_client" {
		status = fiber.StatusUnauthorized
	}
	return c.Status(status).JSON(fiber.Map{
		"error":             errStr,
		"error_description": errors.GetMessage(appCode),
	})
}

// protoInvalidRequest writes an RFC 6749 invalid_request with a caller-supplied
// description (validation failures, where the detail is the useful part).
func protoInvalidRequest(c fiber.Ctx, desc string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"error":             "invalid_request",
		"error_description": desc,
	})
}

// protoServerError writes a 500 for an unexpected internal failure.
//
// The status is the load-bearing part: every RP classifies 5xx as transient
// (keep the session, retry) and 4xx as a verdict on the credential. Answering
// an internal fault with a 400 would tell every client its refresh token is
// permanently dead and log the whole userbase out over a blip. RFC 6749 §5.2
// defines no code for this, so it reuses server_error from §4.1.2.1, which is
// what standard client libraries expect alongside a 5xx.
func protoServerError(c fiber.Ctx) error {
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
		"error":             "server_error",
		"error_description": errors.GetMessage(errors.ErrOperationFailed),
	})
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
	// Pass `prompt` through so the frontend can force re-login (prompt=login),
	// show the account chooser (select_account), or go silent (none).
	// See docs/integration/oauth/07-logout.md + 09-account-switching.md.
	if req.Prompt != "" {
		q.Set("prompt", req.Prompt)
	}
	// login_hint names the account to switch to (multi-account); the OP frontend
	// chooser pre-selects / silently switches to it. See 09-account-switching.md.
	if req.LoginHint != "" {
		q.Set("login_hint", req.LoginHint)
	}
	// nonce (OIDC) is carried through so the consent POST can bind it to the
	// code → echoed into the id_token.
	if req.Nonce != "" {
		q.Set("nonce", req.Nonce)
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
		req.Nonce,
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

// AuthorizeError builds a validated error redirect back to the RP: the user
// denied consent on the OP frontend page, or a prompt=none request needs
// interaction. Public — deny / login_required can occur unauthenticated. The
// redirect_uri is validated against the client's registered URIs here: the
// public /oauth/authorize frontend page is directly reachable, so a crafted
// redirect_uri must never be echoed back unvalidated (open redirect).
func (h *OAuthHandler) AuthorizeError(c fiber.Ctx) error {
	var req dto.AuthorizeErrorRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	if _, err := h.oauthService.ValidateClient(c.Context(), req.ClientID, req.RedirectURI); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	redirectURL, err := buildErrorRedirectURL(req.RedirectURI, req.Error, req.State)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"redirect_url": redirectURL})
}

// Token handles the OAuth token exchange request.
// Supports grant_type=authorization_code and grant_type=refresh_token.
func (h *OAuthHandler) Token(c fiber.Ctx) error {
	var req dto.TokenRequest
	if err := bindByContentType(c, &req); err != nil {
		slog.Warn("oauth token bind failed",
			"content_type", c.Get("Content-Type"),
			"body_len", len(c.Body()),
			"err", err,
		)
		return protoErr(c, errors.ErrBadRequest)
	}
	// client_secret_basic (RFC 6749 §2.3.1): fill client_id/secret from the
	// Basic header when not supplied in the body (client_secret_post wins).
	if id, secret, ok := basicClientAuth(c.Get("Authorization")); ok {
		if req.ClientID == "" {
			req.ClientID = id
		}
		if req.ClientSecret == "" {
			req.ClientSecret = secret
		}
	}

	if err := utils.Validate(&req); err != nil {
		slog.Warn("oauth token validate failed", "grant_type", req.GrantType, "client_id", req.ClientID, "err", err)
		return protoInvalidRequest(c, err.Error())
	}

	switch req.GrantType {
	case "authorization_code":
		tokenResp, err := h.oauthService.ExchangeCode(c.Context(), &req)
		if err != nil {
			if appErr, ok := err.(*errors.AppError); ok {
				return protoErr(c, appErr.Code)
			}
			return protoServerError(c)
		}
		return c.JSON(tokenResp)

	case "refresh_token":
		if req.RefreshToken == "" {
			return protoInvalidRequest(c, "refresh_token is required for grant_type=refresh_token")
		}
		tokenResp, err := h.oauthService.RefreshWithClient(c.Context(), req.RefreshToken, req.ClientID, req.ClientSecret)
		if err != nil {
			if appErr, ok := err.(*errors.AppError); ok {
				return protoErr(c, appErr.Code)
			}
			return protoServerError(c)
		}
		return c.JSON(tokenResp)

	default:
		return protoErr(c, errors.ErrOAuthUnsupportedGrantType)
	}
}

// UserInfo returns user information for the authenticated user.
// Fields are filtered based on the scope embedded in the access token.
func (h *OAuthHandler) UserInfo(c fiber.Ctx) error {
	userUUID := c.Locals("user_uuid").(string)
	scope, _ := c.Locals("user_scope").(string)
	// site_roles in the userinfo response are scoped to the requesting client's
	// site — the same SiteID the access token carries (set into locals by the
	// JWT middleware).
	siteID, _ := c.Locals("user_site").(uint)

	info, err := h.oauthService.GetUserInfo(c.Context(), userUUID, scope, siteID)
	if err != nil {
		// OIDC Core §5.3.3 routes UserInfo failures through RFC 6750 §3, not
		// RFC 6749 §5.2: the caller already authenticated, so a failure here is
		// about the bearer token, not the request grammar.
		if appErr, ok := err.(*errors.AppError); ok {
			slog.Warn("oauth userinfo failed", "user_uuid", userUUID, "code", appErr.Code)
			return middleware.BearerError(c, fiber.StatusUnauthorized, "invalid_token",
				errors.GetMessage(appErr.Code))
		}
		return protoServerError(c)
	}

	return c.JSON(info)
}

// GetClientPublic returns public-safe metadata for an OAuth client.
//
// Consumed by the OAuth web /oauth/authorize page to decide whether to
// render the consent UI (auto_consent=true → silent grant for first-party
// clients) and to show "你将授权访问 X" with the right name + domain.
// No authentication — anyone holding a valid client_id can query the
// metadata (which they obviously do, since they're using it to build the
// authorize URL). Sensitive fields (secret, redirect_uris, scopes, image
// quotas) are intentionally NOT included; see ClientPublicInfo.
func (h *OAuthHandler) GetClientPublic(c fiber.Ctx) error {
	clientID := c.Query("client_id")
	if clientID == "" {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	info, err := h.oauthService.GetClientPublicInfo(c.Context(), clientID)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, info)
}

// GetEcosystem serves the public app directory (GET /oauth/ecosystem): the
// opt-in (listed) clients for the "one KUN Galgame account → these sites" strip
// on register/login. Public + cacheable; only public display fields. See
// docs/integration/oauth/10-app-directory.md.
func (h *OAuthHandler) GetEcosystem(c fiber.Ctx) error {
	apps, err := h.oauthService.ListEcosystem(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"apps": apps})
}

// PostLogoutRedirect validates a post-logout redirect URL against the named
// client's registered redirect_uris (origin match) and returns the safe URL to
// send the browser to, or "" when not allow-listed. Used by the OP logout page
// to guard against open-redirect. No authentication (it only echoes a URL the
// caller already proposed, after validating it). See
// docs/integration/oauth/07-logout.md.
func (h *OAuthHandler) PostLogoutRedirect(c fiber.Ctx) error {
	clientID := c.Query("client_id")
	redirect := c.Query("redirect")
	validated, _ := h.oauthService.ValidatePostLogoutRedirect(c.Context(), clientID, redirect)
	return response.Success(c, fiber.Map{"url": validated})
}

// LogoutRedirect is the RP-initiated logout entrypoint (a browser top-level
// navigation, symmetric with Authorize). It bounces to the OP frontend logout
// page, which clears the central SSO session and redirects back to a validated
// RP URL. Accepts both the first-party params (client_id + redirect) and the
// OIDC RP-Initiated Logout standard ones (post_logout_redirect_uri + state +
// id_token_hint); GET query or POST form, per the spec. The frontend page
// validates the redirect via /oauth/post-logout-redirect before using it and
// echoes `state` onto it. When a standard RP sends only id_token_hint (no
// client_id), the hint's aud names the client to validate against.
// Bouncing through the backend means RPs reuse the same OAuth API base they
// already use for /oauth/authorize, and it resolves correctly in dev (where the
// frontend and API are different origins) via cfg.Server.FrontendURL.
// See docs/integration/oauth/07-logout.md.
func (h *OAuthHandler) LogoutRedirect(c fiber.Ctx) error {
	param := func(key string) string {
		if v := c.Query(key); v != "" {
			return v
		}
		return c.FormValue(key)
	}
	clientID := param("client_id")
	redirect := param("redirect")
	if redirect == "" {
		redirect = param("post_logout_redirect_uri")
	}
	hint := param("id_token_hint")
	if clientID == "" && hint != "" {
		clientID = clientIDFromIDTokenHint(hint)
	}

	q := url.Values{}
	if clientID != "" {
		q.Set("client_id", clientID)
	}
	if redirect != "" {
		q.Set("redirect", redirect)
	}
	if st := param("state"); st != "" {
		q.Set("state", st)
	}
	if hint != "" {
		q.Set("id_token_hint", hint)
	}
	dest := h.cfg.Server.FrontendURL + "/auth/logout"
	if enc := q.Encode(); enc != "" {
		dest += "?" + enc
	}
	return c.Redirect().To(dest)
}

// clientIDFromIDTokenHint extracts the aud (client_id) from an id_token_hint
// WITHOUT verifying the signature: it only selects which client's registered
// redirect_uris the post-logout redirect is validated against, so a forged
// hint gains nothing beyond a redirect that client already allows.
func clientIDFromIDTokenHint(hint string) string {
	tok, _, err := jwt.NewParser().ParseUnverified(hint, jwt.MapClaims{})
	if err != nil {
		return ""
	}
	aud, err := tok.Claims.GetAudience()
	if err != nil || len(aud) == 0 {
		return ""
	}
	return aud[0]
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
		Token         string `json:"token" form:"token" validate:"required"`
		TokenTypeHint string `json:"token_type_hint" form:"token_type_hint"`
	}
	if err := bindByContentType(c, &req); err != nil {
		return protoInvalidRequest(c, "the request body could not be parsed")
	}

	if req.Token == "" {
		return protoInvalidRequest(c, "token is required")
	}

	_ = h.oauthService.RevokeToken(c.Context(), req.Token, req.TokenTypeHint)

	// RFC 7009 §2.2: 200 with an empty body, whether or not the token existed —
	// answering differently would turn this into a token oracle.
	return c.SendStatus(fiber.StatusOK)
}
