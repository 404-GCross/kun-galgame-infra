// Community Service — the multi-tenant discussion primitive (kun_community).
//
// The HTTP surface is code-first OpenAPI 3.1 via Huma layered on Fiber v3,
// following the catalog service's shape (house {code,message,data} envelope,
// path-scoped Fiber S2S auth bridged into Huma).
//
// Face (v0, S2S embed protocol — doc 11 §5.1):
//
//	POST /api/v1/community/comments/resolve   — get-or-create comments thread + posts
//	GET  /api/v1/community/threads            — per-site thread list (keyset)
//	GET  /api/v1/community/threads/{id}        — thread + posts page
//	POST /api/v1/community/topics              — open a board topic
//	POST /api/v1/community/feedback            — open a feedback thread
//	POST /api/v1/community/threads/{id}/posts  — reply
//	POST /api/v1/community/posts/{id}/reaction — toggle a reaction
//	POST /api/v1/community/posts/{id}/flag     — report a post
//	GET  /openapi.json                         — S2S OpenAPI 3.1 spec (no auth)
//	GET  /healthz                              — no auth
//
// The service does NOT run migrations: cmd/migrate-community is the single
// migration entry point; startup only connects.
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/middleware"
	commHandler "api/internal/platform/community/handler"
	"api/internal/platform/community/service"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	health.MaybeProbe(cfg.CommunityService.Port, "/healthz")

	logger.Init(cfg.Server.Env)

	// app.New provides the main-DB connection (OAuth client registry for S2S
	// auth) and the Fiber app; no Redis needed.
	application, err := app.New(cfg, app.Options{Name: "kun-community"})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	communityDB, err := database.NewPostgresDB(cfg.CommunityDatabase)
	if err != nil {
		slog.Error("community db connect", "error", err)
		os.Exit(1)
	}

	// Domain services. Events go to a no-op sink until the notification layer
	// lands (章程 ruling 2 / doc 11 §7).
	sink := service.NoopSink{}
	threadSvc := service.NewThreadService(communityDB.DB(), sink)
	postSvc := service.NewPostService(communityDB.DB(), sink)
	reactionSvc := service.NewReactionService(communityDB.DB())
	feedbackSvc := service.NewFeedbackService(communityDB.DB(), sink)
	flagSvc := service.NewFlagService(communityDB.DB(), sink)
	trustSvc := service.NewTrustService(communityDB.DB())
	reviewSvc := service.NewReviewService(communityDB.DB())

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// S2S face: Basic client credentials, path-scoped before the Huma routes.
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())
	application.Fiber.Use("/api/v1/community", commHandler.S2SAuth(clientRepo))

	api := commHandler.Setup(application.Fiber, threadSvc, postSvc, reactionSvc, feedbackSvc, flagSvc, trustSvc, reviewSvc)

	// Serve the S2S OpenAPI 3.1 spec unauthenticated at the app root.
	application.Fiber.Get("/openapi.json", func(c fiber.Ctx) error {
		b, err := json.Marshal(api.OpenAPI())
		if err != nil {
			return err
		}
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	slog.Info("community service starting",
		"addr", fmt.Sprintf("%s:%d", cfg.CommunityService.Host, cfg.CommunityService.Port),
		"dbname", cfg.CommunityDatabase.DBName,
	)

	defer func() {
		if err := communityDB.Close(); err != nil {
			slog.Error("close community db", "error", err)
		}
	}()

	if err := application.Run(cfg.CommunityService.Host, cfg.CommunityService.Port); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}
