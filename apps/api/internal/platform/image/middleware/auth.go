// Package middleware holds Fiber v3 middleware for the image service:
// client authentication, quota enforcement, and logging helpers.
package middleware

import (
	"crypto/subtle"
	"encoding/base64"
	stderrors "errors"
	"slices"
	"strings"

	siteModel "api/internal/platform/site/model"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// Locals keys written by ClientAuth into fiber.Ctx.
const (
	LocalOAuthClient = "image:oauth_client"
	LocalSiteKey     = "image:site_key"
	LocalUserSub     = "image:user_sub"   // uploader user uuid (JWT path only)
	LocalAuthMethod  = "image:auth_method" // "basic" or "jwt"
)

// ClientHeaderID is the header the frontend includes to name the target
// OAuth client. Used only by the JWT auth path (backend Basic Auth embeds
// the client_id in the auth header itself).
const ClientHeaderID = "X-Kun-Image-Client-Id"

// ClientAuth returns a Fiber v3 middleware that authenticates the caller
// via one of two supported paths:
//
//  1. "Basic <b64(client_id:secret)>" — backend Server-to-Server path.
//     Mirrors OAuth Client Credentials without issuing a token.
//
//  2. "Bearer <user_jwt>" + "X-Kun-Image-Client-Id: <client_id>" header —
//     frontend direct upload. The user JWT must contain `image:upload` in
//     its scope, and the named client must belong to the same SiteID as
//     the JWT (prevents cross-site token misuse).
//
// On success it writes *OAuthClient to Locals under LocalOAuthClient and
// the site key under LocalSiteKey. The auth method used is written to
// LocalAuthMethod so handlers can tell them apart (e.g. for logging).
func ClientAuth(clientRepo *siteRepo.OAuthClientRepository, cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")

		var (
			client *siteModel.OAuthClient
			userSub string
			method string
			err    error
		)

		switch {
		case strings.HasPrefix(authHeader, "Basic "):
			client, err = authenticateBasic(c, clientRepo, authHeader)
			method = "basic"
		case strings.HasPrefix(authHeader, "Bearer "):
			client, userSub, err = authenticateJWT(c, clientRepo, authHeader, cfg)
			method = "jwt"
		default:
			return response.Unauthorized(c, errors.ErrImageUnauthorized)
		}

		if err != nil {
			// Translate known sentinel errors; fall back to generic 401/403.
			var code int
			switch {
			case stderrors.Is(err, errBadClient):
				code = errors.ErrImageBadClient
			case stderrors.Is(err, errBadSecret):
				code = errors.ErrImageBadSecret
			case stderrors.Is(err, errSiteDisabled):
				code = errors.ErrImageSiteDisabled
			case stderrors.Is(err, errSiteUnconfigured):
				code = errors.ErrImageSiteUnconfigured
			default:
				code = errors.ErrImageUnauthorized
			}
			if code == errors.ErrImageSiteDisabled || code == errors.ErrImageSiteUnconfigured {
				return response.Forbidden(c, code)
			}
			return response.Unauthorized(c, code)
		}

		c.Locals(LocalOAuthClient, client)
		c.Locals(LocalSiteKey, client.ImageSiteKey)
		c.Locals(LocalUserSub, userSub)
		c.Locals(LocalAuthMethod, method)
		return c.Next()
	}
}

// ---- auth path implementations ----

var (
	errBadClient        = stderrors.New("bad client")
	errBadSecret        = stderrors.New("bad secret")
	errSiteDisabled     = stderrors.New("site disabled")
	errSiteUnconfigured = stderrors.New("site unconfigured")
	errScopeMissing     = stderrors.New("scope missing")
	errSiteMismatch     = stderrors.New("site mismatch")
)

// authenticateBasic handles the Basic auth backend path.
func authenticateBasic(c fiber.Ctx, repo *siteRepo.OAuthClientRepository, authHeader string) (*siteModel.OAuthClient, error) {
	clientID, secret, err := parseBasicAuth(authHeader)
	if err != nil {
		return nil, errBadClient
	}

	client, err := repo.FindByClientID(c.Context(), clientID)
	if err != nil || client == nil {
		return nil, errBadClient
	}

	if subtle.ConstantTimeCompare([]byte(client.Secret), []byte(secret)) != 1 {
		return nil, errBadSecret
	}

	if !client.ImageEnabled {
		return nil, errSiteDisabled
	}
	if client.ImageSiteKey == "" {
		return nil, errSiteUnconfigured
	}
	return client, nil
}

// authenticateJWT handles the Bearer JWT frontend path.
func authenticateJWT(c fiber.Ctx, repo *siteRepo.OAuthClientRepository, authHeader string, cfg *config.Config) (*siteModel.OAuthClient, string, error) {
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := utils.ParseToken(tokenStr, cfg.JWT.Secret)
	if err != nil {
		return nil, "", errBadClient
	}

	scopes := strings.Fields(claims.Scope)
	if !slices.Contains(scopes, "image:upload") {
		return nil, "", errScopeMissing
	}

	clientID := c.Get(ClientHeaderID)
	if clientID == "" {
		return nil, "", errBadClient
	}

	client, err := repo.FindByClientID(c.Context(), clientID)
	if err != nil || client == nil {
		return nil, "", errBadClient
	}
	if !client.ImageEnabled {
		return nil, "", errSiteDisabled
	}
	if client.ImageSiteKey == "" {
		return nil, "", errSiteUnconfigured
	}

	// Guard: the user's JWT must match the client's site. Otherwise a
	// user with a valid kungal JWT could impersonate as a moyu uploader.
	if client.SiteID != nil && claims.SiteID != 0 && *client.SiteID != claims.SiteID {
		return nil, "", errSiteMismatch
	}

	return client, claims.UserUUID, nil
}

// parseBasicAuth parses an HTTP Basic auth header. Returns the decoded
// user:password pair. Accepts "Basic <b64>" only.
func parseBasicAuth(header string) (user, pass string, err error) {
	if header == "" {
		return "", "", stderrors.New("missing auth header")
	}
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return "", "", stderrors.New("not Basic auth")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return "", "", err
	}
	u, p, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", stderrors.New("malformed Basic auth")
	}
	return u, p, nil
}

// ClientFromCtx is a convenience helper for handlers.
func ClientFromCtx(c fiber.Ctx) *siteModel.OAuthClient {
	v, _ := c.Locals(LocalOAuthClient).(*siteModel.OAuthClient)
	return v
}

// SiteKeyFromCtx is a convenience helper for handlers.
func SiteKeyFromCtx(c fiber.Ctx) string {
	v, _ := c.Locals(LocalSiteKey).(string)
	return v
}

// UserSubFromCtx returns the user uuid when auth was via JWT; "" otherwise.
func UserSubFromCtx(c fiber.Ctx) string {
	v, _ := c.Locals(LocalUserSub).(string)
	return v
}
