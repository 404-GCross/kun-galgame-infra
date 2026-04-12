package middleware

import (
	"strings"

	authService "api/internal/platform/auth/service"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

// Auth middleware validates JWT tokens
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

		// Validate token
		claims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthTokenExpired,
				"message": errors.GetMessage(errors.ErrAuthTokenExpired),
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
