package middleware

import (
	"log/slog"

	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

func DevPortalFence(allowed map[string]bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID, _ := c.Locals("token_client_id").(string)
		if clientID == "" || allowed[clientID] {
			return c.Next()
		}
		slog.Warn("dev portal fence reject", "client_id", clientID, "path", c.Path())
		return response.ForbiddenMsg(c, errors.ErrForbidden,
			"this application is not authorized to access the developer platform")
	}
}
