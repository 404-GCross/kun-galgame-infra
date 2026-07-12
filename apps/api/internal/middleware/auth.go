package middleware

import (
	"log/slog"
	"strings"

	authService "api/internal/platform/auth/service"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

// Auth middleware validates JWT tokens.
//
// In addition to JWT validity, it re-checks the user's current status on
// every request. JWTs are valid for 15 min after issuance, so without
// this DB check a banned user's existing token would keep working until
// natural expiry. The DB hit is a single index lookup by UUID — cheap
// in absolute terms but worth caching (Redis / sync.Map TTL) if QPS
// becomes a concern.
func Auth(authSvc *authService.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthUnauthorized,
				"message": errors.GetMessage(errors.ErrAuthUnauthorized),
			})
		}

		// Check Bearer prefix
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthInvalidToken,
				"message": errors.GetMessage(errors.ErrAuthInvalidToken),
			})
		}

		token := parts[1]

		// Validate token (signature, exp, format)
		claims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			// Debug, not Warn: a naturally-expired 15-min access_token
			// hits this on every user every 15 min — normal, the client
			// then refreshes. Only interesting when chasing a specific bug.
			slog.Debug("auth reject", "stage", "token_invalid", "path", c.Path(), "err", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthTokenExpired,
				"message": errors.GetMessage(errors.ErrAuthTokenExpired),
			})
		}

		// Live status check — JWT might still be valid but the user
		// could have been banned in the last 15 min.
		user, err := authSvc.GetCurrentUser(c.Context(), claims.UserUUID)
		if err != nil || user == nil {
			slog.Warn("auth reject", "stage", "get_current_user",
				"path", c.Path(), "user_uuid", claims.UserUUID, "err", err)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthUserNotFound,
				"message": errors.GetMessage(errors.ErrAuthUserNotFound),
			})
		}
		if user.IsBanned() {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    errors.ErrAuthUserBanned,
				"message": errors.GetMessage(errors.ErrAuthUserBanned),
			})
		}

		// Set user info in context (same local set as the JWTAuth fill sites):
		// user_roles unions the global roles with the token's site_roles, plus the
		// additive user_global_roles / token_client_id — see setIdentityLocals.
		setIdentityLocals(c, claims)

		return c.Next()
	}
}
