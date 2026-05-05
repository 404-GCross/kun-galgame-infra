// Image Service — content-addressed, OAuth-authenticated, per-site
// quota-limited image upload platform.
//
// See docs/image_service/ for the design.
//
// Usage:
//
//   go run ./cmd/image           # 启动服务（要求已经跑过 ./cmd/image-setup）
//
// 首次接入或新环境，先跑一次：
//
//   go run ./cmd/image-setup --seed-test-client
//
// Endpoints (V1+V2):
//   POST /image/upload           — multipart/form-data: file + preset
//   GET  /image/:hash            — metadata lookup
//   GET  /image/stats            — per-site stats
//   POST /image/reference-ping   — JSON: {hashes: [...]}
//   GET  /healthz                — no auth
//   GET  /metrics                — no auth, internal-only by deployment
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/middleware"
	imgHandler "api/internal/platform/image/handler"
	imgMW "api/internal/platform/image/middleware"
	imgModel "api/internal/platform/image/model"
	"api/internal/platform/image/preset"
	"api/internal/platform/image/quota"
	"api/internal/platform/image/repository"
	"api/internal/platform/image/service"
	"api/internal/platform/image/storage"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/logger"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	application, err := app.New(cfg, app.Options{
		Name:      "kun-image",
		NeedCache: true,
	})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("images db connect", "error", err,
			"hint", "若是新环境，请先跑 `go run ./cmd/image-setup`")
		os.Exit(1)
	}
	if err := imagesDB.AutoMigrate(&imgModel.Image{}, &imgModel.ImageSiteUsage{}, &imgModel.ModerationQueue{}); err != nil {
		slog.Error("images automigrate", "error", err)
		os.Exit(1)
	}

	s3Client, err := storage.NewClient(cfg.ImageS3)
	if err != nil {
		slog.Error("s3 init", "error", err)
		os.Exit(1)
	}
	if err := s3Client.EnsureBucket(context.Background()); err != nil {
		slog.Warn("ensure bucket (skip if pre-provisioned)", "error", err)
	}

	presets, err := preset.Load(cfg.ImageService.PresetsPath)
	if err != nil {
		slog.Error("presets load", "error", err, "path", cfg.ImageService.PresetsPath)
		os.Exit(1)
	}

	imgRepo := repository.NewImageRepository(imagesDB.DB())
	usageRepo := repository.NewSiteUsageRepository(imagesDB.DB())
	statsRepo := repository.NewStatsRepository(imagesDB.DB())
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())

	svc := service.New(presets, s3Client, imgRepo, usageRepo, cfg.ImageService.CDNBase)
	q := quota.New(application.Cache)
	h := imgHandler.New(svc, q, statsRepo)

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())

	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Get("/metrics", adaptor.HTTPHandler(promhttp.Handler()))

	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// Upload route is feature-gated. When disabled (the default), return
	// 503 + clear error code without running auth so it's symmetric for
	// any caller. Other endpoints stay on the authenticated group.
	if cfg.ImageService.UploadEnabled {
		application.Fiber.Post("/image/upload",
			imgMW.ClientAuth(clientRepo, cfg),
			h.Upload,
		)
	} else {
		application.Fiber.Post("/image/upload", uploadDisabled)
		slog.Warn("upload disabled (set KUN_IMAGE_UPLOAD_ENABLED=true to allow); other endpoints still serve")
	}

	img := application.Fiber.Group("/image", imgMW.ClientAuth(clientRepo, cfg))
	img.Get("/stats", h.Stats)
	img.Get("/:hash", h.Meta)
	img.Post("/reference-ping", h.Ping)

	addr := fmt.Sprintf("%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
	slog.Info("image service starting",
		"addr", addr,
		"cdn_base", cfg.ImageService.CDNBase,
		"bucket", s3Client.Bucket(),
	)

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

// uploadDisabled is registered in place of the real upload handler when
// KUN_IMAGE_UPLOAD_ENABLED is unset/false. Returns 503 + a clear code so
// callers can branch on it.
func uploadDisabled(c fiber.Ctx) error {
	return response.Error(c,
		fiber.StatusServiceUnavailable,
		errors.ErrImageUploadDisabled,
		errors.GetMessage(errors.ErrImageUploadDisabled),
	)
}
