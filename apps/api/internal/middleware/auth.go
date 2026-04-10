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
		c.Locals("user_roles", claims.Roles)

		return c.Next()
	}
}

// OptionalAuth middleware validates JWT tokens but allows unauthenticated requests.
// It checks the Authorization header first, then falls back to the access_token cookie.
// The cookie fallback is needed for browser redirects (e.g., OAuth /authorize)
// where the browser navigates directly and cannot set custom headers.
func OptionalAuth(authSvc *authService.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		var token string

		// Try Authorization header first
		authHeader := c.Get("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token = parts[1]
			}
		}

		// Fallback: read access_token from cookie (for browser redirects)
		if token == "" {
			token = c.Cookies("access_token")
		}

		if token == "" {
			return c.Next()
		}

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
