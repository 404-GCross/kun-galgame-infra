package middleware

import (
	"errors"
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
		status := statusOf(c, err)

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

// statusOf resolves the status this request will actually answer with.
//
// c.Next() hands back an UNHANDLED error — Fiber's ErrorHandler runs after this
// middleware returns, so at this point the recorded response status is still the
// default 200. Reading it directly therefore logs every unmatched route, and
// every handler that returns an error instead of writing a status itself, as a
// success.
//
// That is not a cosmetic defect. An unmatched route is exactly what a retired
// face becomes, so the log cannot distinguish "this face served the request"
// from "this face is gone" — which is how wave-161 P5's retired /internal/*
// faces answered 404 to ~160k downstream calls a day for three days while every
// log line read `status=200 duration=4µs`. It also invalidates access-log path
// statistics as evidence that a face has no traffic left (the method
// 09-open-api-phase2/07 §78 used to certify the route-B retirement).
//
// A non-fiber error becomes 500, which is what errorHandler will write.
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
