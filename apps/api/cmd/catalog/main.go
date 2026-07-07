// Catalog Service — the cross-media identity/graph registry (kun_catalog).
//
// The HTTP surface is code-first OpenAPI 3.1 via Huma layered on Fiber v3,
// following the artifact service's shape (house {code,message,data} envelope,
// path-scoped Fiber auth middleware bridged into Huma).
//
// Faces (v0):
//
//	POST /api/v1/catalog/resolve            — S2S batch id resolution (Basic client auth)
//	GET  /api/v1/catalog/redirects          — S2S redirect keyset feed (cleanup crons)
//	POST /api/v1/catalog/works/claim        — S2S work claim/registration
//	GET  /api/v1/admin/catalog/*            — admin review queues (JWT + admin role)
//	GET  /openapi.json                       — S2S OpenAPI 3.1 spec (no auth)
//	GET  /healthz                            — no auth
//
// The service does NOT run migrations: cmd/migrate-catalog is the single
// migration entry point; startup only connects and readiness-checks.
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	catHandler "api/internal/platform/catalog/handler"
	"api/internal/platform/catalog/repository"
	catalogSearch "api/internal/platform/catalog/search"
	"api/internal/platform/catalog/service"
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

	health.MaybeProbe(cfg.CatalogService.Port, "/healthz")

	logger.Init(cfg.Server.Env)

	// app.New provides the main-DB connection (OAuth client registry for S2S
	// auth) and the Fiber app; no Redis needed.
	application, err := app.New(cfg, app.Options{Name: "kun-catalog"})
	if err != nil {
		slog.Error("app init", "error", err)
		os.Exit(1)
	}

	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}

	// Domain services (step 05 core).
	redirects := repository.NewRedirectRepository(catalogDB.DB())
	resolveSvc := service.NewResolveService(redirects)
	mergeSvc := service.NewMergeService(catalogDB.DB(), resolveSvc,
		repository.NewProposalRepository(catalogDB.DB()), repository.NewRevisionRepository(catalogDB.DB()))
	workSvc := service.NewWorkService(catalogDB.DB(), resolveSvc)
	queueSvc := service.NewAdminQueueService(catalogDB.DB(), mergeSvc)

	// Read surface (step 18): anchor read-through + credits over the catalog DB,
	// entity search over Meilisearch. NewClient makes no network call — a Meili
	// outage only fails the search endpoint at query time, not startup.
	readSvc := service.NewReadService(catalogDB.DB())
	statsSvc := service.NewStatsService(catalogDB.DB())
	searchClient, err := searchInfra.NewClient(cfg.Meilisearch)
	if err != nil {
		slog.Error("meilisearch client", "error", err)
		os.Exit(1)
	}
	searcher := catalogSearch.NewIndexer(searchClient)

	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// S2S face: Basic client credentials, path-scoped before the Huma routes.
	clientRepo := siteRepo.NewOAuthClientRepository(application.DB.DB())
	application.Fiber.Use("/api/v1/catalog", catHandler.S2SAuth(clientRepo))

	// Admin face: shared JWT middleware (accept-both verifier) + admin role,
	// exactly like the galgame admin surface. The /api/v1/admin/catalog
	// prefix is deliberately disjoint from /api/v1/catalog so the S2S Basic
	// auth never intercepts admin calls.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	application.Fiber.Use("/api/v1/admin/catalog",
		middleware.JWTAuth(tokenVerifier), middleware.RequireRole("admin"))

	s2sAPI := catHandler.Setup(application.Fiber, resolveSvc, workSvc, readSvc, searcher, statsSvc)
	catHandler.SetupAdmin(application.Fiber, queueSvc, mergeSvc)

	// Serve the S2S OpenAPI 3.1 spec unauthenticated at the app root (the
	// auto doc routes are disabled in Setup so they don't land under the
	// authed prefixes).
	application.Fiber.Get("/openapi.json", func(c fiber.Ctx) error {
		b, err := json.Marshal(s2sAPI.OpenAPI())
		if err != nil {
			return err
		}
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	slog.Info("catalog service starting",
		"addr", fmt.Sprintf("%s:%d", cfg.CatalogService.Host, cfg.CatalogService.Port),
		"dbname", cfg.CatalogDatabase.DBName,
	)

	defer func() {
		if err := catalogDB.Close(); err != nil {
			slog.Error("close catalog db", "error", err)
		}
	}()

	if err := application.Run(cfg.CatalogService.Host, cfg.CatalogService.Port); err != nil {
		slog.Error("run", "error", err)
		os.Exit(1)
	}
}
