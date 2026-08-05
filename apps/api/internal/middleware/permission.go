package middleware

import (
	"api/internal/platform/authz"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
)

// RequirePermission gates a route on a single authz permission — the
// permission-first route gate that replaced the role middleware. It reads the
// same c.Locals("user_roles") slice (populated by the JWT middleware) and
// resolves it against res instead of matching role strings. Fail-closed —
// missing or wrong-typed locals, or roles that don't grant p, return 403.
//
// res is an authz.Checker, not a fixed *authz.Resolver: the perm packages hand
// over a Holder, so an overlay refresh that swaps the resolver takes effect at
// gates registered once at startup.
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
