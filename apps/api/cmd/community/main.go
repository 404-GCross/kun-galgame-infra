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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/middleware"
	commHandler "api/internal/platform/community/handler"
	"api/internal/platform/community/service"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/logger"
	"api/pkg/trustclient"

	"github.com/gofiber/fiber/v3"
)

// outboxInterval is the trust-forward sweep cadence (step 03 ruling 2).
const outboxInterval = 60 * time.Second

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

	// Trust S2S client (steps 03/04). Built once; forwarding and scanning both ride
	// it but each has its own switch. Empty KUN_TRUST_CLIENT_* → a nil client →
	// both disabled (fail-closed). Assigning through an interface only when non-nil
	// avoids a typed-nil interface (which would report Enabled()==true then panic).
	trustCli := trustclient.New(trustclient.Config{
		BaseURL: cfg.TrustClient.BaseURL, ClientID: cfg.TrustClient.ClientID, ClientSecret: cfg.TrustClient.ClientSecret,
	})

	var forwarder service.Forwarder
	if trustCli != nil {
		forwarder = trustCli
	}
	forwardSvc := service.NewForwardService(communityDB.DB(), forwarder)
	if forwardSvc.Enabled() {
		slog.Info("community trust forwarding enabled", "base_url", cfg.TrustClient.BaseURL)
	} else {
		slog.Info("community trust forwarding disabled (KUN_TRUST_CLIENT_* unset)")
	}

	// Trust scan sink (step 04). Gated by the INDEPENDENT KUN_TRUST_SCAN_ENABLED
	// switch (default false) AND the trust client being wired — never keyed off the
	// client alone, so a forwarding-configured production community does not
	// auto-enable scanning on deploy (ruling 2).
	var scanner service.Scanner
	if cfg.TrustScanEnabled && trustCli != nil {
		scanner = trustCli
	}
	scanSvc := service.NewScanService(communityDB.DB(), scanner)
	if scanSvc.Enabled() {
		slog.Info("community trust scanning enabled", "base_url", cfg.TrustClient.BaseURL)
	} else {
		slog.Info("community trust scanning disabled (KUN_TRUST_SCAN_ENABLED off or KUN_TRUST_CLIENT_* unset)")
	}

	// Trust check gate (step 06). Gated by the INDEPENDENT KUN_TRUST_CHECK_ENABLED
	// switch (default false) AND the trust client being wired — same principle as
	// scanning. This runs SYNCHRONOUSLY before a write commits (deny blocks, hold
	// enqueues) but fails OPEN on any error/timeout, so a down trust service never
	// blocks posting.
	var checker service.Checker
	if cfg.TrustCheckEnabled && trustCli != nil {
		checker = trustCli
	}
	checkSvc := service.NewCheckService(checker)
	if checkSvc.Enabled() {
		slog.Info("community trust check enabled (synchronous word-list gate)", "base_url", cfg.TrustClient.BaseURL)
	} else {
		slog.Info("community trust check disabled (KUN_TRUST_CHECK_ENABLED off or KUN_TRUST_CLIENT_* unset)")
	}

	// Domain services. Notification events still go to a no-op sink (章程 ruling 2
	// / doc 11 §7); the ForwardingSink turns the step-03 review.* events into
	// off-request trust forward/resolve calls, and the ScanningSink turns
	// post.created / post.edited into step-04 shadow-scan calls. The two decorators
	// consume disjoint events and nest in any order.
	sink := service.NewScanningSink(service.NewForwardingSink(service.NoopSink{}, forwardSvc), scanSvc)
	threadSvc := service.NewThreadService(communityDB.DB(), sink, service.WithThreadChecker(checkSvc))
	postSvc := service.NewPostService(communityDB.DB(), sink, service.WithPostChecker(checkSvc))
	reactionSvc := service.NewReactionService(communityDB.DB())
	feedbackSvc := service.NewFeedbackService(communityDB.DB(), sink)
	flagSvc := service.NewFlagService(communityDB.DB(), sink)
	trustSvc := service.NewTrustService(communityDB.DB())
	reviewSvc := service.NewReviewService(communityDB.DB(), sink)
	callbackSvc := service.NewCallbackService(communityDB.DB())

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// Trust enforcement callback (step 03). OUTSIDE the /api/v1/community S2S
	// Basic-auth prefix — it authenticates with the trust HMAC instead. Empty
	// KUN_TRUST_CALLBACK_SECRET → every callback 401s (fail-closed).
	application.Fiber.Post("/trust/callback", commHandler.TrustCallback(cfg.TrustCallbackSecret, callbackSvc))

	// S2S face: Basic client credentials, path-scoped before the Huma routes.
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())
	application.Fiber.Use("/api/v1/community", commHandler.S2SAuth(clientRepo))

	api := commHandler.Setup(application.Fiber, threadSvc, postSvc, reactionSvc, feedbackSvc, flagSvc, trustSvc, reviewSvc)

	// Outbox ticker: sweeps un-forwarded review items (step 03 ruling 2). Idles
	// when forwarding is disabled (Sweep is a no-op then).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runOutboxTicker(ctx, forwardSvc)

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

// runOutboxTicker sweeps un-forwarded review items on the outbox interval until
// ctx is cancelled. Errors are logged, never fatal.
func runOutboxTicker(ctx context.Context, fwd *service.ForwardService) {
	t := time.NewTicker(outboxInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := fwd.Sweep(ctx); err != nil {
				slog.Error("community trust-forward sweep", "err", err)
			} else if n > 0 {
				slog.Info("community trust-forward sweep", "forwarded", n)
			}
		}
	}
}
