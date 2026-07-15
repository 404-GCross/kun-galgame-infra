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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"api/internal/app"
	"api/internal/infrastructure/cache"
	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	catHandler "api/internal/platform/catalog/handler"
	catalogPerm "api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/repository"
	catalogSearch "api/internal/platform/catalog/search"
	"api/internal/platform/catalog/service"
	"api/internal/platform/devapi"
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

	// Admin face: shared JWT middleware (accept-both verifier) + catalog.review
	// permission (ren), exactly like the galgame admin surface. The
	// /api/v1/admin/catalog prefix is deliberately disjoint from /api/v1/catalog
	// so the S2S Basic auth never intercepts admin calls.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	application.Fiber.Use("/api/v1/admin/catalog",
		middleware.JWTAuth(tokenVerifier), middleware.RequirePermission(catalogPerm.Resolver, catalogPerm.Review))

	s2sAPI := catHandler.Setup(application.Fiber, resolveSvc, workSvc, readSvc, searcher, statsSvc)
	catHandler.SetupAdmin(application.Fiber, queueSvc, mergeSvc)

	// NextMoe open API: catalog public projection (/v1/catalog/*). A NEW public
	// read-only bypass (step 03) behind the shared devapi middleware chain; the
	// S2S (/api/v1/catalog) and admin (/api/v1/admin/catalog) faces are untouched
	// (disjoint prefixes). Probable anchors and r18 works never surface.
	setupPublicCatalog(application, cfg, catalogDB, readSvc, resolveSvc, searcher)

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

// setupPublicCatalog mounts the /v1/catalog public projection group behind the
// devapi middleware chain, wires per-response usage metering (face="catalog"),
// and starts the usage flush lifecycle (60s ticker + a final flush on graceful
// shutdown, run before the main DB is closed). Mirrors the galgame public face
// (step 02): Redis is a soft dependency (its outage fails rate-limit/quota open,
// never blocks boot or the S2S face); the DB credential check never fails open.
func setupPublicCatalog(
	application *app.App,
	cfg *config.Config,
	catalogDB *database.PostgresDB,
	readSvc *service.ReadService,
	resolveSvc *service.ResolveService,
	searcher *catalogSearch.Indexer,
) {
	oauthDB := application.DB.DB() // kun_galgame_infra — the developer-platform tables

	// devapi counter/cache store: reuse the shared Redis when reachable, else
	// fail open. Built here rather than via app NeedCache so a Redis outage can
	// NEVER block the catalog service from booting or affect the S2S face.
	var devCache *cache.RedisCache
	if rc, err := cache.NewRedisCache(cfg.Redis); err != nil {
		slog.Warn("devapi: redis unavailable — rate-limit/quota will fail open", "err", err)
	} else {
		devCache = rc
	}
	store := devapi.NewRedisStore(devCache)
	repo := devapi.NewRepository(oauthDB)
	mw := devapi.NewMiddleware(repo, store)
	usageRec := devapi.NewUsageRecorder(repo, store)

	publicSvc := service.NewPublicService(catalogDB.DB(), readSvc, resolveSvc)
	publicH := catHandler.NewPublicHandler(publicSvc, resolveSvc, searcher)

	// Meter every response to (client, key, "catalog", day) + async last-used
	// touch. Placed right after ResolveCredential so a 401 (no/invalid key) is not
	// billed, while a 429/403 (authenticated-but-limited) IS captured.
	recordUsage := func(c fiber.Ctx) error {
		err := c.Next()
		if cred := devapi.CredentialFrom(c); cred != nil {
			usageRec.Record(cred, "catalog", c.Response().StatusCode())
			go usageRec.TouchLastUsed(context.Background(), cred)
		}
		return err
	}

	v1 := application.Fiber.Group("/v1/catalog",
		mw.ResolveCredential,
		recordUsage,
		mw.RateLimit,
		mw.Quota,
		devapi.RequireScope(devapi.ScopeCatalogRead),
	)

	// External-id reverse-lookup (killer) + batch; the batch path is registered
	// before the GET so the /lookup namespace is unambiguous.
	v1.Get("/lookup", publicH.Lookup)
	v1.Post("/lookup/batch", publicH.LookupBatch)
	v1.Post("/resolve", publicH.Resolve)
	v1.Get("/redirects", publicH.Redirects)
	v1.Get("/search", publicH.Search)
	v1.Get("/works/:id", publicH.WorkDetail)
	v1.Get("/names/:id", publicH.Name)
	v1.Get("/characters/:id", publicH.Character)
	v1.Get("/labels/:id", publicH.Label)

	// Usage flush lifecycle: a 60s ticker upserts the in-memory rollup; a final
	// flush runs on graceful shutdown via OnPreShutdown, which fires during
	// Fiber.Shutdown() BEFORE the main DB is closed, so the last batch is not lost.
	flushDone := make(chan struct{})
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-flushDone:
				return
			case <-t.C:
				if err := usageRec.Flush(context.Background()); err != nil {
					slog.Warn("devapi usage flush failed", "err", err)
				}
			}
		}
	}()
	application.Fiber.Hooks().OnPreShutdown(func() error {
		close(flushDone)
		if err := usageRec.Flush(context.Background()); err != nil {
			slog.Warn("devapi final usage flush failed", "err", err)
		}
		if devCache != nil {
			_ = devCache.Close()
		}
		return nil
	})
}
