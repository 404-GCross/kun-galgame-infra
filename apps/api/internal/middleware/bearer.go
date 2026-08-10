package middleware

import (
	"fmt"
	"log/slog"
	"strings"

	authService "api/internal/platform/auth/service"

	"github.com/gofiber/fiber/v3"
)

const bearerRealm = "kungal"

func splitBearer(header string) (token string, ok bool) {
	scheme, rest, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return rest, true
}

func bearerChallenge(c fiber.Ctx, errCode, desc string) {
	if errCode == "" {
		c.Set(fiber.HeaderWWWAuthenticate, fmt.Sprintf("Bearer realm=%q", bearerRealm))
		return
	}
	c.Set(fiber.HeaderWWWAuthenticate,
		fmt.Sprintf("Bearer realm=%q, error=%q, error_description=%q", bearerRealm, errCode, desc))
}

func BearerError(c fiber.Ctx, status int, errCode, desc string) error {
	bearerChallenge(c, errCode, desc)
	if errCode == "" {
		c.Status(status)
		return nil
	}
	return c.Status(status).JSON(fiber.Map{
		"error":             errCode,
		"error_description": desc,
	})
}

func BearerAuth(authSvc *authService.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get(fiber.HeaderAuthorization)
		if authHeader == "" {
			return BearerError(c, fiber.StatusUnauthorized, "", "")
		}

		token, ok := splitBearer(authHeader)
		if !ok || token == "" {
			return BearerError(c, fiber.StatusBadRequest, "invalid_request",
				"Authorization header must use the Bearer scheme")
		}

		claims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			slog.Debug("bearer reject", "stage", "token_invalid", "path", c.Path(), "err", err)
			return BearerError(c, fiber.StatusUnauthorized, "invalid_token",
				"The access token is expired, revoked or malformed")
		}

		user, err := authSvc.GetCurrentUser(c.Context(), claims.UserUUID)
		if err != nil || user == nil {
			slog.Warn("bearer reject", "stage", "get_current_user",
				"path", c.Path(), "user_uuid", claims.UserUUID, "err", err)
			return BearerError(c, fiber.StatusUnauthorized, "invalid_token",
				"The access token does not identify a known user")
		}
		if user.IsBanned() {
			return BearerError(c, fiber.StatusForbidden, "invalid_token",
				"The account is banned")
		}

		setIdentityLocals(c, claims)

		return c.Next()
	}
}
