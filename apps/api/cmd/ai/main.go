// AI Gateway — the semantic layer that fronts all ecosystem AI calls behind
// named routes (doc 20). v0 ships ONE route (moderate-text); the route registry
// (internal/platform/ai/route) admits a new route as one table row. This
// process is the single holder of the upstream credential, meters per
// tenant×route usage into kun_ai, and enforces per-route daily budget fuses.
//
// The HTTP surface is code-first OpenAPI 3.1 via Huma on Fiber v3, following
// the trust/community shape (house {code,message,data} envelope, path-scoped
// Fiber S2S Basic auth bridged into Huma).
//
// Faces (v0):
//
//	POST /api/v1/ai/moderate-text   — S2S text scan (Basic client auth; fail-open)
//	GET  /api/v1/admin/ai/*         — usage dashboard (JWT + ai.usage_view; admin/ren)
//	GET  /openapi.json              — S2S OpenAPI 3.1 spec (no auth)
//	GET  /healthz                   — no auth
//
// Upstream decoupling (doc 20 §9 red-line): the ONLY knowledge of the channel
// layer is KUN_AI_UPSTREAM_BASE_URL + KUN_AI_UPSTREAM_TOKEN. Empty → degraded
// mode: moderate-text returns fail-open (degraded:true/flagged:false) without
// dialling, and startup is NEVER blocked. The service does NOT run migrations:
// cmd/migrate-ai is the single migration entry point; startup only connects.
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
	aiHandler "api/internal/platform/ai/handler"
	aiPerm "api/internal/platform/ai/perm"
	"api/internal/platform/ai/service"
	"api/internal/platform/ai/upstream"
	"api/internal/platform/permissions"
	siteRepo "api/internal/platform/site/repository"
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

	health.MaybeProbe(cfg.AIService.Port, "/healthz")

	logger.Init(cfg.Server.Env)

	// app.New provides the main-DB connection (OAuth client registry for S2S
	// auth + catalog_site tenant derivation) and the Fiber app; no Redis
	// (NeedCache:false — the budget fuse is the only soft gate, computed from
	// SQL, so v0 needs no rate limiter).
	application, err := app.New(cfg, app.Options{Name: "kun-ai"})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	aiDB, err := database.NewPostgresDB(cfg.AIDatabase)
	if err != nil {
		slog.Error("ai db connect", "error", err)
		os.Exit(1)
	}

	// Tier2 LLM upstream client. Empty base URL/token = that tier off — a valid
	// state, not an error: the service returns fail-open without dialling. Never
	// blocks startup (doc 20 §9).
	up := upstream.NewClient(cfg.AIUpstream.BaseURL, cfg.AIUpstream.Token, cfg.AIUpstream.Model)
	if up.Configured() {
		slog.Info("ai llm (tier2) configured", "base_url", cfg.AIUpstream.BaseURL, "model", cfg.AIUpstream.Model)
	} else {
		slog.Warn("ai llm (tier2) NOT configured — moderate-text runs Tier1 alone / degraded")
	}

	// Tier1 omni-moderation coarse pass (spec 09). Empty token = Tier1 off:
	// moderate-text runs the Tier2 LLM path only. Also never blocks startup.
	omni := upstream.NewOmniClient(cfg.AIOmni.BaseURL, cfg.AIOmni.Token, cfg.AIOmni.Model)
	if omni.Configured() {
		slog.Info("ai omni (tier1) configured", "base_url", cfg.AIOmni.BaseURL, "model", cfg.AIOmni.Model,
			"escalate_threshold", cfg.AIOmni.EscalateThreshold, "negative_sample_rate", cfg.AIOmni.NegativeSampleRate)
	} else {
		slog.Warn("ai omni (tier1) NOT configured — moderate-text runs the Tier2 LLM path only")
	}

	moderationSvc := service.NewModerationService(aiDB.DB(), omni, up, service.ModerationOptions{
		EscalateThreshold:  cfg.AIOmni.EscalateThreshold,
		NegativeSampleRate: cfg.AIOmni.NegativeSampleRate,
	})
	statsSvc := service.NewStatsService(aiDB.DB())
	budgetSvc := service.NewBudgetService(aiDB.DB())

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// S2S face: Basic client credentials, path-scoped before the Huma routes.
	// The /api/v1/ai prefix is disjoint from /api/v1/admin/ai so the S2S Basic
	// auth never intercepts admin calls.
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())
	application.Fiber.Use("/api/v1/ai", aiHandler.S2SAuth(clientRepo))

	// Admin face: shared JWT middleware (accept-both verifier) + the
	// ai.usage_view permission (admin/ren; NOT moderator — usage cost is ops),
	// exactly like the trust admin surface but with no per-site scoping.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	application.Fiber.Use("/api/v1/admin/ai",
		middleware.JWTAuth(tokenVerifier), middleware.RequirePermission(aiPerm.Resolver, aiPerm.UsageView))

	s2sAPI := aiHandler.Setup(application.Fiber, moderationSvc)
	aiHandler.SetupAdmin(application.Fiber, statsSvc, budgetSvc)

	// Serve the S2S OpenAPI 3.1 spec unauthenticated at the app root.
	application.Fiber.Get("/openapi.json", func(c fiber.Ctx) error {
		b, err := json.Marshal(s2sAPI.OpenAPI())
		if err != nil {
			return err
		}
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	permCtx, cancelPerm := context.WithCancel(context.Background())
	defer cancelPerm()
	// Permission overlay (docs/auth/04 §7). This service enforces its own
	// domain's keys, and those keys can be widened at runtime by the permission
	// console, so it must keep its Resolver current. It reads the overlay
	// straight from the main database it already holds a connection to; with no
	// Redis in this process the refresh runs on the poll interval, which is the
	// floor that makes the overlay reliable everywhere.
	permissions.NewDistributor(application.DB.DB(), permissions.Live(), nil).Start(permCtx)

	slog.Info("ai service starting",
		"addr", fmt.Sprintf("%s:%d", cfg.AIService.Host, cfg.AIService.Port),
		"dbname", cfg.AIDatabase.DBName,
	)

	defer func() {
		if err := aiDB.Close(); err != nil {
			slog.Error("close ai db", "error", err)
		}
	}()

	if err := application.Run(cfg.AIService.Host, cfg.AIService.Port); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}
