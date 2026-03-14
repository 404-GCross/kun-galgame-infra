package middleware

import (
	"slices"

	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

// RequireRole middleware checks if user has required role
func RequireRole(roles ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		userRoles, ok := c.Locals("user_roles").([]string)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    errors.ErrForbidden,
				"message": errors.GetMessage(errors.ErrForbidden),
			})
		}

		// Check if user has any of the required roles
		for _, required := range roles {
			if slices.Contains(userRoles, required) {
				return c.Next()
			}
		}

		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"code":    errors.ErrForbidden,
			"message": errors.GetMessage(errors.ErrForbidden),
		})
	}
}

// RequireOwnerOrRole middleware checks if user owns the resource or has required role
func RequireOwnerOrRole(ownerUUIDKey string, roles ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		userUUID, ok := c.Locals("user_uuid").(string)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    errors.ErrForbidden,
				"message": errors.GetMessage(errors.ErrForbidden),
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
				if slices.Contains(userRoles, required) {
					return c.Next()
				}
			}
		}

		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"code":    errors.ErrForbidden,
			"message": errors.GetMessage(errors.ErrForbidden),
		})
	}
}
