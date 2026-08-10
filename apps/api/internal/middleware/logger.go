package middleware

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v3"
)

func Logger() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()

		err := c.Next()

		duration := time.Since(start)
		status := statusOf(c, err)

		attrs := []any{
			"method", c.Method(),
			"path", c.Path(),
			"status", status,
			"duration", duration.String(),
			"ip", c.IP(),
		}

		if requestID := c.Locals("request_id"); requestID != nil {
			attrs = append(attrs, "request_id", requestID)
		}

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

func statusOf(c fiber.Ctx, err error) int {
	if err == nil {
		return c.Response().StatusCode()
	}
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	return fiber.StatusInternalServerError
}
