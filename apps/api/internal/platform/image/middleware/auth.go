// Package middleware holds Fiber v3 middleware for the image service:
// client authentication, quota enforcement, and logging helpers.
package middleware

import (
	"crypto/subtle"
	"encoding/base64"
	stderrors "errors"
	"strings"

	siteModel "api/internal/platform/site/model"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// Locals keys written by ClientAuth into fiber.Ctx.
const (
	LocalOAuthClient = "image:oauth_client"
	LocalSiteKey     = "image:site_key"
)

// ClientAuth returns a Fiber v3 middleware that authenticates the caller as
// an OAuth Client via HTTP Basic Auth with (client_id, client_secret).
//
// On success it writes the matched *siteModel.OAuthClient to c.Locals under
// LocalOAuthClient for downstream handlers.
//
// On failure it returns a 401/403 through pkg/response.
//
// Design note: image service uses Basic Auth instead of running a full
// client_credentials grant because (a) no existing OAuth code path issues
// machine tokens, and (b) Basic Auth is idiomatic for S2S internal APIs.
func ClientAuth(clientRepo *siteRepo.OAuthClientRepository) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID, secret, err := parseBasicAuth(c.Get("Authorization"))
		if err != nil {
			return response.Unauthorized(c, errors.ErrImageUnauthorized)
		}

		client, err := clientRepo.FindByClientID(c.Context(), clientID)
		if err != nil || client == nil {
			return response.Unauthorized(c, errors.ErrImageBadClient)
		}

		if subtle.ConstantTimeCompare([]byte(client.Secret), []byte(secret)) != 1 {
			return response.Unauthorized(c, errors.ErrImageBadSecret)
		}

		if !client.ImageEnabled {
			return response.Forbidden(c, errors.ErrImageSiteDisabled)
		}
		if client.ImageSiteKey == "" {
			return response.Forbidden(c, errors.ErrImageSiteUnconfigured)
		}

		c.Locals(LocalOAuthClient, client)
		c.Locals(LocalSiteKey, client.ImageSiteKey)
		return c.Next()
	}
}

// parseBasicAuth parses an HTTP Basic auth header. Returns the decoded
// user:password pair. Accepts "Basic <b64>" only (not Bearer).
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
