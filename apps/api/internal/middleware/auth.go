package middleware

import (
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
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthTokenExpired,
				"message": errors.GetMessage(errors.ErrAuthTokenExpired),
			})
		}

		// Live status check — JWT might still be valid but the user
		// could have been banned in the last 15 min.
		user, err := authSvc.GetCurrentUser(c.Context(), claims.UserUUID)
		if err != nil || user == nil {
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

		// Set user info in context
		c.Locals("user_uuid", claims.UserUUID)
		c.Locals("user_uid", claims.UID)
		c.Locals("user_roles", claims.Roles)
		c.Locals("user_scope", claims.Scope)

		return c.Next()
	}
}
