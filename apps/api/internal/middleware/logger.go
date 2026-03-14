package middleware

import (
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
)

// Logger middleware logs request information
func Logger() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()

		// Process request
		err := c.Next()

		// Log request
		duration := time.Since(start)
		status := c.Response().StatusCode()

		attrs := []any{
			"method", c.Method(),
			"path", c.Path(),
			"status", status,
			"duration", duration.String(),
			"ip", c.IP(),
		}

		// Add request ID if available
		if requestID := c.Locals("request_id"); requestID != nil {
			attrs = append(attrs, "request_id", requestID)
		}

		// Add user UUID if available
		if userUUID := c.Locals("user_uuid"); userUUID != nil {
			attrs = append(attrs, "user_uuid", userUUID)
		}

		if status >= 500 {
			slog.Error("request completed", attrs...)
		} else if status >= 400 {
			slog.Warn("request completed", attrs...)
		} else {
			slog.Info("request completed", attrs...)
		}

		return err
	}
}
