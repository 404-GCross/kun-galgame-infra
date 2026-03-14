package middleware

import (
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

const RequestIDHeader = "X-Request-ID"

// RequestID middleware adds a unique request ID to each request
func RequestID() fiber.Handler {
	return func(c fiber.Ctx) error {
		// Check if request ID already exists
		requestID := c.Get(RequestIDHeader)
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Set request ID in context and response header
		c.Locals("request_id", requestID)
		c.Set(RequestIDHeader, requestID)

		return c.Next()
	}
}
