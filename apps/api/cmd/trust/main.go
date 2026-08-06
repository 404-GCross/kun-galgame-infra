// Trust & Safety Service — the universal report-intake + unified review inbox
// platform (kun_trust), doc 18 P0.
//
// The HTTP surface is code-first OpenAPI 3.1 via Huma layered on Fiber v3,
// following the catalog/community shape (house {code,message,data} envelope,
// path-scoped Fiber auth bridged into Huma).
//
// Faces (v0):
//
//	POST /api/v1/trust/reports          — S2S report intake (Basic client auth)
//	POST /api/v1/trust/scan             — S2S content-scan intake (AI shadow-scoring)
//	GET  /api/v1/trust/subject-kinds    — S2S: the site's registered kinds
//	GET  /api/v1/admin/trust/*          — admin review inbox (JWT + trust.queue_access)
//	GET  /openapi.json                   — S2S OpenAPI 3.1 spec (no auth)
//	GET  /healthz                        — no auth
//
// Two background goroutines run: the enforcement-callback dispatcher (HMAC-signed
// webhooks, exponential backoff, dead-letter) and the AI shadow-scoring pipeline
// (drains pending scan rows via the AI gateway's moderate-text route; env-empty =
// degraded drain, never a review item — shadow only). The service does NOT run
// migrations: cmd/migrate-trust is the single migration entry point; startup only
// connects.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/middleware"
	"api/internal/platform/permissions"
	siteRepo "api/internal/platform/site/repository"
	trustHandler "api/internal/platform/trust/handler"
	trustPerm "api/internal/platform/trust/perm"
	"api/internal/platform/trust/service"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/logger"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}

	health.MaybeProbe(cfg.TrustService.Port, "/healthz")

	logger.Init(cfg.Server.Env)

	// app.New provides the main-DB connection (OAuth client registry for S2S
	// auth + the reporter-weight users/roles lookup) and the Fiber app; no
	// Redis needed (章程 ruling 6 — SQL window count, not Redis).
	application, err := app.New(cfg, app.Options{Name: "kun-trust"})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	trustDB, err := database.NewPostgresDB(cfg.TrustDatabase)
	if err != nil {
		slog.Error("trust db connect", "error", err)
		os.Exit(1)
	}

	// Domain services. The reporter weigher reads the main DB (users + roles).
	weigher := service.NewDBWeigher(application.DB.DB())

	// Per-site moderation posture (step 07 M0). ONE instance is shared by the
	// scan worker, the report intake and the admin face, so an operator's write
	// invalidates the cache every reader is looking at rather than each holding
	// its own stale copy. The env/constant values below are the PLATFORM
	// DEFAULTS: a site with no policy row resolves to exactly them, which is why
	// introducing the table changes nothing until somebody writes to it.
	policySvc := service.NewPolicyService(trustDB.DB(), service.PlatformDefaults{
		ScanMode:   service.ScanModeFromName(cfg.TrustScanMode),
		SampleRate: cfg.TrustScanSampleRate,
		// The one default that is a code constant rather than an env var; it moves
		// into the table the first time a site needs a different number.
		AggregateThreshold: service.DefaultAggregateThreshold(),
		// Live has always implied auto-hide, so that is what a site inherits.
		AutoHideEnabled: true,
	})

	reportSvc := service.NewReportService(trustDB.DB(), weigher, service.WithReportPolicy(policySvc))
	reviewSvc := service.NewReviewService(trustDB.DB())
	registrySvc := service.NewRegistryService(trustDB.DB())
	dispositionSvc := service.NewDispositionService(trustDB.DB())
	worker := service.NewCallbackWorker(trustDB.DB())

	// community→trust forward face (step 03). The allowlist is the counterweight
	// to forward carrying `site` in its body; an empty allowlist (default) makes
	// forward/resolve 403 for every client (fail-closed, ruling 3).
	forwarders := make(map[string]bool, len(cfg.TrustForwarderClientIDs))
	for _, id := range cfg.TrustForwarderClientIDs {
		forwarders[id] = true
	}
	forwardSvc := service.NewForwardService(trustDB.DB(), forwarders)
	// The scan face shares the same allowlist: a wire-supplied `site` (relay path,
	// step 04) is allow-listed exactly like a forward, while the default bind-
	// derived path is always open.
	scanSvc := service.NewScanService(trustDB.DB(), forwarders)
	slog.Info("trust forward face", "allowed_forwarders", len(forwarders))

	// Tier0 word list (step 05). One TermService instance is shared across the
	// sync /trust/check face, the admin CRUD face, and the scan worker's
	// tier0_matched recording — so an admin mutation invalidates the in-process
	// match cache for all three at once. It reuses the forwarder allowlist as the
	// counterweight to a wire-supplied `site` on /trust/check.
	termSvc := service.NewTermService(trustDB.DB(), forwarders)

	// AI shadow-scoring pipeline (step 03). The scan worker scores pending rows
	// via the AI gateway's moderate-text route (S2S Basic). Empty KUN_AI_CLIENT_*
	// → the gateway client is not Configured() → the worker drains rows to
	// degraded WITHOUT dialing (fail-closed; the queue never backs up). It also
	// records Tier0 word-list matches into tier0_matched before each gateway call
	// (step 05; pure recording, never changes status).
	// KUN_TRUST_SCAN_MODE=live (wave 07) additionally lets a FLAGGED verdict open an
	// ai_text review item and queue a hide disposition for the callback worker to
	// deliver; anything else keeps the original record-only shadow posture. Since
	// step 07 M0 that env is the PLATFORM DEFAULT: it governs every site that has
	// not set its own posture in trust_site_policy.
	aiGateway := service.NewAIGatewayClient(cfg.AIClient.BaseURL, cfg.AIClient.ClientID, cfg.AIClient.ClientSecret)
	scanWorker := service.NewScanWorker(trustDB.DB(), aiGateway, termSvc,
		service.WithScanMode(cfg.TrustScanMode),
		service.WithSampleRate(cfg.TrustScanSampleRate),
		service.WithPolicy(policySvc))
	slog.Info("trust scan worker", "gateway_configured", aiGateway.Configured(), "default_mode", cfg.TrustScanMode)

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// S2S intake face: Basic client credentials, path-scoped before the Huma
	// routes. The /api/v1/trust prefix is disjoint from /api/v1/admin/trust so
	// the S2S Basic auth never intercepts admin calls.
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())
	application.Fiber.Use("/api/v1/trust", trustHandler.S2SAuth(clientRepo))

	// Admin face: shared JWT middleware (accept-both verifier) + the
	// trust.queue_access permission (moderator/admin/ren), exactly like the
	// catalog admin surface.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	application.Fiber.Use("/api/v1/admin/trust",
		middleware.JWTAuth(tokenVerifier), middleware.RequirePermission(trustPerm.Resolver, trustPerm.QueueAccess))

	s2sAPI := trustHandler.Setup(application.Fiber, reportSvc, registrySvc, forwardSvc, scanSvc, termSvc)
	// clientRepo (main DB) resolves a site-scoped moderator's token client to its
	// catalog_site for admin-face site scoping (step 04). termSvc backs the Tier0
	// admin CRUD surface (step 05).
	trustHandler.SetupAdmin(application.Fiber, reviewSvc, registrySvc, dispositionSvc, termSvc, policySvc, clientRepo)

	// Serve the S2S OpenAPI 3.1 spec unauthenticated at the app root.
	application.Fiber.Get("/openapi.json", func(c fiber.Ctx) error {
		b, err := json.Marshal(s2sAPI.OpenAPI())
		if err != nil {
			return err
		}
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	// Background workers: the enforcement-callback dispatch worker (章程 ruling 9)
	// and the AI shadow-scoring pipeline (step 03).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Permission overlay (docs/auth/04 §7). This service enforces its own
	// domain's keys, and those keys can be widened at runtime by the permission
	// console, so it must keep its Resolver current. It reads the overlay
	// straight from the main database it already holds a connection to; with no
	// Redis in this process the refresh runs on the poll interval, which is the
	// floor that makes the overlay reliable everywhere.
	permissions.NewDistributor(application.DB.DB(), permissions.Live(), nil).Start(ctx)

	go worker.Run(ctx)
	go scanWorker.Run(ctx)

	slog.Info("trust service starting",
		"addr", fmt.Sprintf("%s:%d", cfg.TrustService.Host, cfg.TrustService.Port),
		"dbname", cfg.TrustDatabase.DBName,
	)

	defer func() {
		if err := trustDB.Close(); err != nil {
			slog.Error("close trust db", "error", err)
		}
	}()

	if err := application.Run(cfg.TrustService.Host, cfg.TrustService.Port); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}
