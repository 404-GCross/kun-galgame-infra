package middleware

import (
	"strings"

	"api/pkg/errors"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// JWTAuth is a lightweight JWT middleware that only requires the JWT secret.
// Unlike Auth(), it does not depend on AuthService — suitable for services
// that only need to verify tokens without full auth capabilities.
func JWTAuth(jwtSecret string) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthUnauthorized,
				"message": errors.GetMessage(errors.ErrAuthUnauthorized),
			})
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthInvalidToken,
				"message": errors.GetMessage(errors.ErrAuthInvalidToken),
			})
		}

		claims, err := utils.ParseToken(parts[1], jwtSecret)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthTokenExpired,
				"message": errors.GetMessage(errors.ErrAuthTokenExpired),
			})
		}

		c.Locals("user_uuid", claims.UserUUID)
		c.Locals("user_roles", claims.Roles)

		return c.Next()
	}
}
