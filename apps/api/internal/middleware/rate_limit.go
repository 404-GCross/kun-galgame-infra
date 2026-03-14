package middleware

import (
	"time"

	"api/internal/infrastructure/cache"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
)

// RateLimit middleware limits request rate per IP
func RateLimit(redisCache *cache.RedisCache) fiber.Handler {
	config := limiter.Config{
		Max:        100,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    429,
				"message": "too many requests",
			})
		},
	}

	// Use redis storage if available
	if storage := redisCache.Storage(); storage != nil {
		config.Storage = storage
	}

	return limiter.New(config)
}

// StrictRateLimit creates a stricter rate limiter for sensitive endpoints
func StrictRateLimit(redisCache *cache.RedisCache) fiber.Handler {
	config := limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":" + c.Path()
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    429,
				"message": "too many requests, please try again later",
			})
		},
	}

	// Use redis storage if available
	if storage := redisCache.Storage(); storage != nil {
		config.Storage = storage
	}

	return limiter.New(config)
}
