// Artifact Service — OAuth-authenticated, per-site, private-bucket large-file
// upload/download platform (Backblaze B2 + Cloudflare). Clients upload/download
// directly to B2 via presigned URLs; the service never handles file bytes.
//
// See docs/artifact/ for the design.
//
// Endpoints (Phase 1):
//
//	POST   /api/v1/artifacts                 — InitUpload   (gated by KUN_ARTIFACT_UPLOAD_ENABLED)
//	POST   /api/v1/artifacts/:uuid/complete  — CompleteUpload (gated)
//	GET    /api/v1/artifacts                  — List (site-scoped)
//	GET    /api/v1/artifacts/:uuid            — Get metadata
//	GET    /api/v1/artifacts/:uuid/download   — issue download URL
//	DELETE /api/v1/artifacts/:uuid            — soft delete
//	GET    /healthz                           — no auth
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/middleware"
	artHandler "api/internal/platform/artifact/handler"
	artMW "api/internal/platform/artifact/middleware"
	artModel "api/internal/platform/artifact/model"
	"api/internal/platform/artifact/quota"
	"api/internal/platform/artifact/repository"
	"api/internal/platform/artifact/service"
	"api/internal/platform/artifact/storage"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/errors"
	"api/pkg/health"
	"api/pkg/logger"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	health.MaybeProbe(cfg.ArtifactService.Port, "/healthz")

	logger.Init(cfg.Server.Env)

	application, err := app.New(cfg, app.Options{
		Name:      "kun-artifact",
		NeedCache: true,
	})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	artifactsDB, err := database.NewPostgresDB(cfg.ArtifactsDatabase)
	if err != nil {
		slog.Error("artifacts db connect", "error", err)
		os.Exit(1)
	}
	if err := artifactsDB.AutoMigrate(&artModel.Artifact{}, &artModel.Manifest{}); err != nil {
		slog.Error("artifacts automigrate", "error", err)
		os.Exit(1)
	}

	s3Client, err := storage.NewClient(cfg.ArtifactS3)
	if err != nil {
		slog.Error("s3 init", "error", err)
		os.Exit(1)
	}
	if err := s3Client.EnsureBucket(context.Background()); err != nil {
		slog.Warn("ensure bucket (skip if pre-provisioned)", "error", err)
	}

	repo := repository.NewArtifactRepository(artifactsDB.DB())
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())
	q := quota.New(application.Cache)
	svc := service.New(repo, s3Client, q, service.Options{
		MultipartThreshold: cfg.ArtifactService.MultipartThreshold,
		PartSize:           cfg.ArtifactService.PartSize,
		PresignUploadTTL:   cfg.ArtifactService.PresignUploadTTL,
		PresignDownloadTTL: cfg.ArtifactService.PresignDownloadTTL,
	})
	h := artHandler.New(svc)

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	arts := application.Fiber.Group("/api/v1/artifacts", artMW.ClientAuth(clientRepo, cfg))
	arts.Get("/", h.List)
	arts.Get("/:uuid", h.Get)
	arts.Get("/:uuid/download", h.Download)
	arts.Delete("/:uuid", h.Delete)

	if cfg.ArtifactService.UploadEnabled {
		arts.Post("/", h.InitUpload)
		arts.Post("/:uuid/complete", h.CompleteUpload)
	} else {
		arts.Post("/", uploadDisabled)
		arts.Post("/:uuid/complete", uploadDisabled)
		slog.Warn("upload disabled (set KUN_ARTIFACT_UPLOAD_ENABLED=true to allow); other endpoints still serve")
	}

	slog.Info("artifact service starting",
		"addr", fmt.Sprintf("%s:%d", cfg.ArtifactService.Host, cfg.ArtifactService.Port),
		"bucket", s3Client.Bucket(),
		"upload_enabled", cfg.ArtifactService.UploadEnabled,
	)

	defer func() {
		if err := artifactsDB.Close(); err != nil {
			slog.Error("close artifacts db", "error", err)
		}
	}()

	if err := application.Run(cfg.ArtifactService.Host, cfg.ArtifactService.Port); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}

// uploadDisabled stands in for the upload handlers when KUN_ARTIFACT_UPLOAD_ENABLED
// is unset/false. Returns 503 + a clear code so callers can branch on it.
func uploadDisabled(c fiber.Ctx) error {
	return response.Error(c,
		fiber.StatusServiceUnavailable,
		errors.ErrArtifactUploadDisabled,
		errors.GetMessage(errors.ErrArtifactUploadDisabled),
	)
}
