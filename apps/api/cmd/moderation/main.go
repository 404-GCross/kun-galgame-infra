package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/logger"

	moderationHandler "api/internal/platform/moderation/handler"
	moderationRepo "api/internal/platform/moderation/repository"
	moderationService "api/internal/platform/moderation/service"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Init(cfg.Server.Env)

	application, err := app.New(cfg, app.Options{
		Name:      "kun-moderation",
		NeedCache: false,
	})
	if err != nil {
		slog.Error("failed to create application", "error", err)
		os.Exit(1)
	}

	setupRoutes(application, cfg)

	port := getPort("KUN_MODERATION_PORT", 9281)
	if err := application.Run(cfg.Server.Host, port); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}

func setupRoutes(a *app.App, cfg *config.Config) {
	db := a.DB.DB()

	// Repositories
	moderationRepository := moderationRepo.NewModerationRepository(db)

	// Services
	moderationSvc := moderationService.NewModerationService(moderationRepository, nil)

	// Handlers
	moderationH := moderationHandler.NewModerationHandler(moderationSvc)

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())
	a.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// API routes
	api := a.Fiber.Group("/api")
	v1 := api.Group("/v1")

	v1.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Moderation routes (JWT auth + admin/moderator role)
	moderation := v1.Group("/moderation",
		middleware.JWTAuth(cfg.JWT.Secret),
		middleware.RequireRole("admin", "moderator"),
	)
	moderation.Get("/jobs", moderationH.ListJobs)
	moderation.Get("/jobs/:id", moderationH.GetJob)
	moderation.Post("/jobs/:id/review", moderationH.ManualReview)
	moderation.Get("/policies", moderationH.ListPolicies)
}

func getPort(envKey string, defaultPort int) int {
	if v := os.Getenv(envKey); v != "" {
		p := 0
		for _, c := range v {
			p = p*10 + int(c-'0')
		}
		if p > 0 {
			return p
		}
	}
	return defaultPort
}
