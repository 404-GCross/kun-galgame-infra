package middleware

import (
	"strings"

	authService "api/internal/platform/auth/service"

	"github.com/gofiber/fiber/v3"
)

// Auth middleware validates JWT tokens
func Auth(authSvc *authService.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    401,
				"message": "missing authorization header",
			})
		}

		// Check Bearer prefix
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    401,
				"message": "invalid authorization header format",
			})
		}

		token := parts[1]

		// Validate token
		claims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    401,
				"message": "invalid or expired token",
			})
		}

		// Set user info in context
		c.Locals("user_uuid", claims.UserUUID)
		c.Locals("user_roles", claims.Roles)

		return c.Next()
	}
}

// OptionalAuth middleware validates JWT tokens but allows unauthenticated requests
func OptionalAuth(authSvc *authService.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		// Get authorization header
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Next()
		}

		// Check Bearer prefix
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Next()
		}

		token := parts[1]

		// Validate token
		claims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			return c.Next()
		}

		// Set user info in context
		c.Locals("user_uuid", claims.UserUUID)
		c.Locals("user_roles", claims.Roles)

		return c.Next()
	}
}
