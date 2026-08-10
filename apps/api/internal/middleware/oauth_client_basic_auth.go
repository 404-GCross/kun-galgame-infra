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

const LocalOAuthClient = "oauth:client"

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

func OAuthClientFromCtx(c fiber.Ctx) *siteModel.OAuthClient {
	v, _ := c.Locals(LocalOAuthClient).(*siteModel.OAuthClient)
	return v
}
