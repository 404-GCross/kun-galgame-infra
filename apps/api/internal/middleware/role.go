package middleware

import (
	"github.com/gofiber/fiber/v3"
)

// RequireRole middleware checks if user has required role
func RequireRole(roles ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		userRoles, ok := c.Locals("user_roles").([]string)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    403,
				"message": "access denied",
			})
		}

		// Check if user has any of the required roles
		for _, required := range roles {
			for _, userRole := range userRoles {
				if userRole == required {
					return c.Next()
				}
			}
		}

		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"code":    403,
			"message": "insufficient permissions",
		})
	}
}

// RequireOwnerOrRole middleware checks if user owns the resource or has required role
func RequireOwnerOrRole(ownerUUIDKey string, roles ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		userUUID, ok := c.Locals("user_uuid").(string)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    403,
				"message": "access denied",
			})
		}

		// Check if user is owner
		ownerUUID := c.Locals(ownerUUIDKey)
		if ownerUUID != nil && ownerUUID.(string) == userUUID {
			return c.Next()
		}

		// Check roles
		userRoles, ok := c.Locals("user_roles").([]string)
		if ok {
			for _, required := range roles {
				for _, userRole := range userRoles {
					if userRole == required {
						return c.Next()
					}
				}
			}
		}

		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"code":    403,
			"message": "insufficient permissions",
		})
	}
}
