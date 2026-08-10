package middleware

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

func RequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		requestID := c.Get(RequestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}

		c.Locals("request_id", requestID)
		c.Set(RequestIDHeader, requestID)

		return c.Next()
	}
}
