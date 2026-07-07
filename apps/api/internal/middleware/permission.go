package middleware

import (
	"api/internal/platform/authz"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

// RequirePermission gates a route on a single authz permission. It is the
// permission-first counterpart of RequireRole: it reads the same
// c.Locals("user_roles") slice (populated by the JWT middleware) and resolves
// it against res instead of matching role strings. Fail-closed — missing or
// wrong-typed locals, or roles that don't grant p, return 403.
func RequirePermission(res *authz.Resolver, p authz.Permission) fiber.Handler {
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
