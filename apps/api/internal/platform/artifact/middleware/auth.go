// Package middleware holds Fiber v3 middleware for the artifact service:
// client authentication mirroring the image service (Basic S2S + Bearer JWT).
package middleware

import (
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
	LocalOAuthClient = "artifact:oauth_client"
	LocalSiteKey     = "artifact:site_key"
	LocalUserSub     = "artifact:user_sub"    // uploader user uuid (JWT path only)
	LocalAuthMethod  = "artifact:auth_method" // "basic" or "jwt"
)

// ClientHeaderID is the header the frontend includes to name the target OAuth
// client (JWT path only; Basic embeds the client_id in the auth header).
const ClientHeaderID = "X-Kun-Artifact-Client-Id"

// uploadScope is the OAuth scope a user JWT must carry to upload.
const uploadScope = "artifact:upload"

// ClientAuth authenticates the caller via either path:
//
//  1. "Basic <b64(client_id:secret)>" — backend S2S (OAuth Client Credentials
//     without issuing a token).
//  2. "Bearer <user_jwt>" + "X-Kun-Artifact-Client-Id: <client_id>" — frontend
//     direct upload. The JWT must carry the `artifact:upload` scope and the
//     named client must belong to the same SiteID as the JWT (fail-closed).
func ClientAuth(clientRepo *siteRepo.OAuthClientRepository, cfg *config.Config) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")

		var (
			client  *siteModel.OAuthClient
			userSub string
			method  string
			err     error
		)

		switch {
		case strings.HasPrefix(authHeader, "Basic "):
			client, err = authenticateBasic(c, clientRepo, authHeader)
			method = "basic"
		case strings.HasPrefix(authHeader, "Bearer "):
			client, userSub, err = authenticateJWT(c, clientRepo, authHeader, cfg)
			method = "jwt"
		default:
			return response.Unauthorized(c, errors.ErrArtifactUnauthorized)
		}

		if err != nil {
			var code int
			switch {
			case stderrors.Is(err, errBadClient):
				code = errors.ErrArtifactBadClient
			case stderrors.Is(err, errBadSecret):
				code = errors.ErrArtifactBadSecret
			case stderrors.Is(err, errSiteDisabled):
				code = errors.ErrArtifactSiteDisabled
			case stderrors.Is(err, errSiteUnconfigured):
				code = errors.ErrArtifactSiteUnconfigured
			default:
				code = errors.ErrArtifactUnauthorized
			}
			if code == errors.ErrArtifactSiteDisabled || code == errors.ErrArtifactSiteUnconfigured {
				return response.Forbidden(c, code)
			}
			return response.Unauthorized(c, code)
		}

		c.Locals(LocalOAuthClient, client)
		c.Locals(LocalSiteKey, client.ArtifactSiteKey)
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

func authenticateBasic(c fiber.Ctx, repo *siteRepo.OAuthClientRepository, authHeader string) (*siteModel.OAuthClient, error) {
	clientID, secret, err := parseBasicAuth(authHeader)
	if err != nil {
		return nil, errBadClient
	}
	client, err := repo.FindByClientID(c.Context(), clientID)
	if err != nil || client == nil {
		return nil, errBadClient
	}
	if !client.VerifySecret(secret) {
		return nil, errBadSecret
	}
	if !client.ArtifactEnabled {
		return nil, errSiteDisabled
	}
	if client.ArtifactSiteKey == "" {
		return nil, errSiteUnconfigured
	}
	return client, nil
}

func authenticateJWT(c fiber.Ctx, repo *siteRepo.OAuthClientRepository, authHeader string, cfg *config.Config) (*siteModel.OAuthClient, string, error) {
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
	claims, err := utils.ParseToken(tokenStr, cfg.JWT.Secret)
	if err != nil {
		return nil, "", errBadClient
	}

	scopes := strings.Fields(claims.Scope)
	if !slices.Contains(scopes, uploadScope) {
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
	if !client.ArtifactEnabled {
		return nil, "", errSiteDisabled
	}
	if client.ArtifactSiteKey == "" {
		return nil, "", errSiteUnconfigured
	}

	// Fail-closed: the user's JWT must match the client's site. Any missing
	// piece is a rejection, never a bypass (mirrors the image service).
	if client.SiteID == nil || claims.SiteID == 0 || *client.SiteID != claims.SiteID {
		return nil, "", errSiteMismatch
	}
	return client, claims.UserUUID, nil
}

func parseBasicAuth(header string) (user, pass string, err error) {
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

// ClientFromCtx returns the authenticated OAuth client.
func ClientFromCtx(c fiber.Ctx) *siteModel.OAuthClient {
	v, _ := c.Locals(LocalOAuthClient).(*siteModel.OAuthClient)
	return v
}

// SiteKeyFromCtx returns the authenticated site key.
func SiteKeyFromCtx(c fiber.Ctx) string {
	v, _ := c.Locals(LocalSiteKey).(string)
	return v
}

// UserSubFromCtx returns the uploader user uuid when auth was via JWT; "" else.
func UserSubFromCtx(c fiber.Ctx) string {
	v, _ := c.Locals(LocalUserSub).(string)
	return v
}
