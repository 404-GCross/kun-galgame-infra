// Image Service — content-addressed, OAuth-authenticated, per-site
// quota-limited image upload platform.
//
// See docs/image_service/ for the design.
//
// Endpoints (V1):
//   POST /image/upload           — multipart/form-data: file + preset
//   GET  /image/:hash            — metadata lookup
//   POST /image/reference-ping   — JSON: {hashes: [...]}
//   GET  /healthz                — no auth
//   GET  /metrics                — internal only (prometheus, TODO)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	imgHandler "api/internal/platform/image/handler"
	imgMW "api/internal/platform/image/middleware"
	imgModel "api/internal/platform/image/model"
	"api/internal/platform/image/preset"
	"api/internal/platform/image/quota"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/service"
	"api/internal/platform/image/storage"
	siteRepo "api/internal/platform/site/repository"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	// Build the app with the oauth database (for oauth_clients lookup).
	application, err := app.New(cfg, app.Options{
		Name:      "kun-image",
		NeedCache: true,
	})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	// Connect a second DB (images-specific).
	imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("images db connect", "error", err)
		os.Exit(1)
	}

	// GORM auto-migrate images-side tables.
	if err := imagesDB.AutoMigrate(&imgModel.Image{}, &imgModel.ImageSiteUsage{}); err != nil {
		slog.Error("images automigrate", "error", err)
		os.Exit(1)
	}

	// Auto-migrate the extended OAuthClient columns on the main DB
	// (callers' client records need the image_* fields).
	// NOTE: we don't call AutoMigrate on site.OAuthClient here to avoid
	// touching other tables — the main oauth migrate cmd handles it.

	// S3 client.
	s3Client, err := storage.NewClient(cfg.ImageS3)
	if err != nil {
		slog.Error("s3 init", "error", err)
		os.Exit(1)
	}
	if err := s3Client.EnsureBucket(context.Background()); err != nil {
		slog.Warn("ensure bucket (dev convenience)", "error", err)
	}

	// Presets config.
	presets, err := preset.Load(cfg.ImageService.PresetsPath)
	if err != nil {
		slog.Error("presets load", "error", err, "path", cfg.ImageService.PresetsPath)
		os.Exit(1)
	}

	// Repositories.
	imgRepo := repository.NewImageRepository(imagesDB.DB())
	usageRepo := repository.NewSiteUsageRepository(imagesDB.DB())
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())

	// Service + handler.
	svc := service.New(presets, s3Client, imgRepo, usageRepo, cfg.ImageService.CDNBase)
	q := quota.New(application.Cache)
	h := imgHandler.New(svc, q)

	// Global middleware.
	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())

	// CORS — V1 intentionally off (no frontend direct upload). Leaving a
	// note here for V2 when we open it up.

	// Health & metrics (no auth).
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Authenticated image API.
	img := application.Fiber.Group("/image", imgMW.ClientAuth(clientRepo))
	img.Post("/upload", h.Upload)
	img.Get("/:hash", h.Meta)
	img.Post("/reference-ping", h.Ping)

	addr := fmt.Sprintf("%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
	slog.Info("image service starting",
		"addr", addr,
		"cdn_base", cfg.ImageService.CDNBase,
		"bucket", s3Client.Bucket(),
	)

	// Close extra DB on shutdown. app.Run handles the primary DB + cache.
	defer func() {
		if err := imagesDB.Close(); err != nil {
			slog.Error("close images db", "error", err)
		}
	}()

	if err := application.Run(cfg.ImageService.Host, cfg.ImageService.Port); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}
