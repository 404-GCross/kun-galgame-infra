package middleware

import (
	"api/internal/platform/authz"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

func RequirePermission(res authz.Checker, p authz.Permission) fiber.Handler {
	return func(c fiber.Ctx) error {
		roles, ok := c.Locals("user_roles").([]string)
		if !ok || !res.Can(roles, p) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"code":    errors.ErrForbidden,
				"message": errors.GetMessage(errors.ErrForbidden),
			})
		}
		return c.Next()
	}
}
