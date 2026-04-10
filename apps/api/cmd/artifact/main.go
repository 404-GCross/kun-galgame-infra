package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/logger"

	artifactHandler "api/internal/platform/artifact/handler"
	artifactRepo "api/internal/platform/artifact/repository"
	artifactService "api/internal/platform/artifact/service"

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
		Name:      "kun-artifact",
		NeedCache: false,
	})
	if err != nil {
		slog.Error("failed to create application", "error", err)
		os.Exit(1)
	}

	setupRoutes(application, cfg)

	port := getPort("KUN_ARTIFACT_PORT", 9279)
	if err := application.Run(cfg.Server.Host, port); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}

func setupRoutes(a *app.App, cfg *config.Config) {
	db := a.DB.DB()

	// Repositories
	artifactRepository := artifactRepo.NewArtifactRepository(db)

	// Services
	artifactSvc := artifactService.NewArtifactService(artifactRepository)

	// Handlers
	artifactH := artifactHandler.NewArtifactHandler(artifactSvc)

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

	// Artifact routes (JWT auth, no full AuthService needed)
	artifacts := v1.Group("/artifacts", middleware.JWTAuth(cfg.JWT.Secret))
	artifacts.Get("/", artifactH.List)
	artifacts.Get("/:id", artifactH.Get)
	artifacts.Post("/", artifactH.Create)
	artifacts.Delete("/:id", artifactH.Delete)
	artifacts.Get("/:id/download", artifactH.Download)
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
