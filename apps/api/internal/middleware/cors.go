package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
)

// CORS middleware handles Cross-Origin Resource Sharing.
// frontendOrigin accepts a comma-separated list of origins, e.g.
// "http://127.0.0.1:9420,http://127.0.0.1:9421".
func CORS(frontendOrigin string) fiber.Handler {
	origins := []string{
		"https://kungal.com",
		"https://moyu.moe",
	}
	for _, o := range strings.Split(frontendOrigin, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}

	return cors.New(cors.Config{
		AllowOrigins: origins,
		AllowMethods: []string{
			fiber.MethodGet,
			fiber.MethodPost,
			fiber.MethodPut,
			fiber.MethodPatch,
			fiber.MethodDelete,
			fiber.MethodOptions,
		},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Accept",
			"Authorization",
			"X-Request-ID",
		},
		ExposeHeaders: []string{
			"X-Request-ID",
		},
		AllowCredentials: true,
		MaxAge:           86400,
	})
}
