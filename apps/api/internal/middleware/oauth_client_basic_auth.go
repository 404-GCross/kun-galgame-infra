package middleware

import (
	"encoding/base64"
	stderrors "errors"
	"strings"

	siteModel "api/internal/platform/site/model"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// LocalOAuthClient is the fiber.Ctx Locals key under which OAuthClientBasicAuth
// stores the authenticated *siteModel.OAuthClient for downstream handlers.
const LocalOAuthClient = "oauth:client"

// OAuthClientBasicAuth authenticates a service-to-service caller by HTTP Basic
// Auth using OAuth client_id / client_secret stored in the `oauth_clients`
// table. Intended for internal cross-service endpoints (like
// `GET /api/v1/users/batch`) called by kungal / moyu / galgame_wiki backends.
//
// Pattern matches `internal/platform/image/middleware.ClientAuth` but without
// image-specific feature gates (image_enabled / preset whitelist) — this is
// for endpoints any registered OAuth Client can call.
//
// Stores the matched *OAuthClient under c.Locals(LocalOAuthClient).
func OAuthClientBasicAuth(repo *siteRepo.OAuthClientRepository) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID, secret, err := parseBasicAuth(c.Get("Authorization"))
		if err != nil {
			return response.Unauthorized(c, errors.ErrAuthUnauthorized)
		}

		client, err := repo.FindByClientID(c.Context(), clientID)
		if err != nil || client == nil {
			return response.Unauthorized(c, errors.ErrOAuthInvalidClient)
		}

		if !client.VerifySecret(secret) {
			return response.Unauthorized(c, errors.ErrOAuthInvalidClientSecret)
		}

		c.Locals(LocalOAuthClient, client)
		return c.Next()
	}
}

// parseBasicAuth decodes "Basic <b64(user:pass)>" auth headers. Returns
// the user/pass pair or a sentinel error.
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

// OAuthClientFromCtx fetches the authenticated client (set by OAuthClientBasicAuth)
// from a Fiber context. Returns nil if not authenticated.
func OAuthClientFromCtx(c fiber.Ctx) *siteModel.OAuthClient {
	v, _ := c.Locals(LocalOAuthClient).(*siteModel.OAuthClient)
	return v
}
