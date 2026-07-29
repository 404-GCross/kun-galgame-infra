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
	"api/internal/galgameapp"
	"api/internal/infrastructure/cache"
	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	"api/internal/platform/catalog/editspec"
	catHandler "api/internal/platform/catalog/handler"
	catalogPerm "api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/repository"
	catalogSearch "api/internal/platform/catalog/search"
	"api/internal/platform/catalog/service"
	"api/internal/platform/devapi"
	"api/internal/platform/editing"
	galgameEditspec "api/internal/platform/galgame/editspec"
	galgameHandler "api/internal/platform/galgame/handler"
	galgamePerm "api/internal/platform/galgame/perm"
	galgameSearch "api/internal/platform/galgame/search"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/imageclient"
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

	// galgame content DB — a SECOND, independent connection pool for the galgame
	// surface co-hosted here after the wiki-retirement W2 merge. cfg.GalgameDatabase
	// (KUN_GALGAME_PG_DATABASE) points at kun_catalog too post-W1, so this is a
	// distinct pool onto the same database the S2S catalog face reads. Kept separate
	// (not catalogDB) so the galgame wiring stays byte-identical to the retired
	// standalone galgame service (W2 replay-verified).
	galgameDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("galgame db connect", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := galgameDB.Close(); err != nil {
			slog.Error("close galgame db", "error", err)
		}
	}()

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

	// Editing engine (E0): the media-agnostic edit_proposal primitive. This
	// is the ASSEMBLY POINT (charter ruling 1) — the engine itself knows no
	// entity family; each family registers its EntityTypeSpec here with
	// closures over its own pool. Pilot: catalog.work (three scalar fields).
	// The engine tables live on the catalog pool; the face registers under
	// /api/v1/catalog/edit/* so the S2S Basic auth above already gates it.
	editRegistry := editing.NewRegistry()
	if err := editspec.RegisterWork(editRegistry, catalogDB.DB()); err != nil {
		slog.Error("editing: register catalog.work", "error", err)
		os.Exit(1)
	}
	// galgame.game (E2a): the galgame family's registration carries the
	// galgame pool in its closures; the engine tables stay on the catalog
	// pool. The same engine instance serves the S2S edit face AND the
	// galgame surface's strangler adapter (Mount below).
	//
	// OnMerge (E3b-tail) reindexes Meilisearch on the single write path so an
	// engine-path edit (kungal BFF → catalog edit face → engine) is searchable
	// immediately — the gap that let game 5794's DB name drift ahead of the
	// index. The same hook the galgame handlers use (search.Hook.Galgame,
	// fire-and-forget). Built here so RegisterGame carries it; Mount builds its
	// own hook for the surviving Create/Update/taxonomy handlers.
	galgameReindex := galgameSearch.NewHook(galgameDB.DB(), galgameSearch.NewIndexer(searchClient))
	if err := galgameEditspec.RegisterGame(editRegistry, galgameDB.DB(), galgameReindex.Galgame); err != nil {
		slog.Error("editing: register galgame.game", "error", err)
		os.Exit(1)
	}
	editEngine := editing.NewEngine(catalogDB.DB(), editRegistry)
	// Per-family perm resolvers (E3a ruling 1): the generic edit face routes
	// an asserted actor's roles through the vocabulary of the entity's own
	// family — registered here alongside the EntityTypeSpecs, so the face
	// hardcodes no family name and the engine stays family-agnostic.
	catHandler.SetupEdit(s2sAPI, editEngine, catHandler.PermResolvers{
		"catalog": catalogPerm.Resolver,
		"galgame": galgamePerm.Resolver,
	})

	// NextMoe open API: serve the two frozen public specs unauthenticated at
	// their face roots — the machine-readable contract itself must not need a
	// key. Built ONCE at boot through the same spec-only Setup functions
	// cmd/gen-openapi uses, so the served JSON always matches the frozen
	// Tier-A YAML the CI freeze gates pin. Registered BEFORE the /v1 groups
	// below: an exact GET route outranks their prefix middleware, so these
	// two paths bypass the devapi key chain while everything else under /v1
	// stays keyed.
	catalogSpec, err := json.Marshal(catHandler.SetupCatalogPublicSpec(fiber.New()).OpenAPI())
	if err != nil {
		slog.Error("marshal catalog public spec", "error", err)
		os.Exit(1)
	}
	galgameSpec, err := json.Marshal(galgameHandler.SetupGalgamePublicSpec(fiber.New()).OpenAPI())
	if err != nil {
		slog.Error("marshal galgame public spec", "error", err)
		os.Exit(1)
	}
	serveSpec := func(path string, body []byte) {
		application.Fiber.Get(path, func(c fiber.Ctx) error {
			c.Set("Content-Type", "application/json")
			c.Set("Cache-Control", "public, max-age=3600")
			return c.Send(body)
		})
	}
	serveSpec("/v1/catalog/openapi.json", catalogSpec)
	serveSpec("/v1/galgame/openapi.json", galgameSpec)

	// NextMoe open API: catalog public projection (/v1/catalog/*). A NEW public
	// read-only bypass (step 03) behind the shared devapi middleware chain; the
	// S2S (/api/v1/catalog) and admin (/api/v1/admin/catalog) faces are untouched
	// (disjoint prefixes). Probable anchors and r18 works never surface.
	setupPublicCatalog(application, cfg, catalogDB, readSvc, resolveSvc, searcher)

	// Host the full galgame HTTP surface (wiki-retirement W2; SOLE host since
	// the standalone galgame service retired at W3) — internal /api/galgame|tag|
	// official|engine|series + admin + S2S cron feeds + the /v1/galgame public
	// projection — registered on this process, reading galgameDB (kun_catalog).
	// Disjoint route prefixes from the catalog faces (/api/v1/catalog,
	// /api/v1/admin/catalog, /v1/catalog); the shared global middleware +
	// /healthz were already installed above, so Mount does not re-register them.
	galgameapp.Mount(application, cfg, galgameapp.Deps{
		OAuthDB:   application.DB.DB(),
		GalgameDB: galgameDB.DB(),
		Search:    searchClient,
		Edit:      editEngine,
		EditDB:    catalogDB.DB(),
	})

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

	publicSvc := service.NewPublicService(catalogDB.DB(), readSvc, resolveSvc, cfg.ImageService.CDNBase)
	// Cover enrichment (A2-1a): resolve dimensions + thumbhash from
	// image_service so the public covers carry no-CLS metadata and the works
	// list can tell a portrait from a banner. Same client config galgameapp
	// builds for the galgame face — a second instance rather than a shared one
	// because Mount owns its client privately and runs after this; the SDK is
	// stateless beyond its HTTP pool, so two instances cost nothing. Unset
	// credentials leave enrichment off, which degrades gracefully.
	if cfg.ImageClient.ClientID != "" && cfg.ImageClient.ClientSecret != "" {
		imgCli := imageclient.New(imageclient.Config{
			BaseURL:      cfg.ImageClient.BaseURL,
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     cfg.ImageClient.ClientID,
			ClientSecret: cfg.ImageClient.ClientSecret,
		})
		publicSvc.WithImageMeta(func(ctx context.Context, hashes []string) (map[string]service.ImageMeta, error) {
			raw, err := imgCli.MetaBatch(ctx, hashes)
			if err != nil {
				return nil, err
			}
			out := make(map[string]service.ImageMeta, len(raw))
			for h, m := range raw {
				out[h] = service.ImageMeta{Width: m.Width, Height: m.Height, Thumbhash: m.Thumbhash}
			}
			return out, nil
		})
	} else {
		slog.Warn("catalog public face: image client not configured — covers will carry no dimensions/thumbhash and the banner slot stays null")
	}
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
	// Works browse lane + changes feed (doc 106 W1): static paths registered
	// before the /works/:id catch-all.
	v1.Get("/works", publicH.WorksList)
	v1.Get("/changes", publicH.Changes)
	// Release-calendar buckets (A2-1c): month view + the two pending buckets.
	v1.Get("/calendar", publicH.Calendar)
	v1.Get("/calendar/pending", publicH.CalendarPending)
	v1.Get("/calendar/tba", publicH.CalendarTBA)
	// Taxonomy browse lanes (A2-1b), each registered before its own /:id.
	v1.Get("/labels", publicH.LabelsList)
	v1.Get("/tags", publicH.TagsList)
	v1.Get("/engines", publicH.EnginesList)
	v1.Get("/works/:id", publicH.WorkDetail)
	v1.Get("/names/:id", publicH.Name)
	v1.Get("/characters/:id", publicH.Character)
	v1.Get("/labels/:id", publicH.Label)
	v1.Get("/tags/:id", publicH.Tag)
	v1.Get("/engines/:id", publicH.EngineDetail)

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
