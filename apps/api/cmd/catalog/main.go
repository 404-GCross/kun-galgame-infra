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
	"api/internal/platform/permissions"
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

	// Admin face: shared JWT middleware (accept-both verifier) + the catalog
	// admin gate, which routes on the path — catalog.review (ren) for registry
	// curation, catalog.claim.review (moderator+) for the claim review queue
	// (wave 157). The /api/v1/admin/catalog prefix is deliberately disjoint from
	// /api/v1/catalog so the S2S Basic auth never intercepts admin calls.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	application.Fiber.Use("/api/v1/admin/catalog",
		middleware.JWTAuth(tokenVerifier), catHandler.AdminGate())

	s2sAPI := catHandler.Setup(application.Fiber, resolveSvc, workSvc, readSvc, searcher, statsSvc)
	claimSvc := service.NewClaimLifecycleService(catalogDB.DB())
	catHandler.SetupAdmin(application.Fiber, queueSvc, mergeSvc, claimSvc,
		service.NewImageReferenceService(catalogDB.DB()))

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
	// The vocabulary layer (wave 154, 03 定案 §4): catalog.{label,tag,engine,
	// series} as narrow field-edit registrations. Registering them here is all
	// it takes for the generic /internal/edit face to serve them — which is the
	// point of a family-agnostic engine, and why retiring the two taxonomy CRUD
	// consoles costs no replacement UI on this side.
	if err := editspec.RegisterTaxonomy(editRegistry, catalogDB.DB()); err != nil {
		slog.Error("editing: register catalog taxonomy families", "error", err)
		os.Exit(1)
	}
	// galgame.game (E2a) is NO LONGER REGISTERED (wave 161 P5).
	//
	// This is the last write path into the galgame tables, and it is not a
	// face — it is an entity_type the generic S2S edit face accepts. Taking
	// down the HTTP surfaces without unregistering it would leave
	// POST /api/v1/catalog/edit/proposals with entity_type=galgame.game as a
	// fully working way to write tables the window is about to DROP, which is
	// exactly the hole SW-E1 exists to close: after this deploy nothing can
	// mint or mutate a galgame row, so the edit-history rekey that follows can
	// never be overtaken.
	//
	// Its two OnMerge hooks die with it: the Meilisearch galgame reindex (whose
	// indexes retire with the family) and the wave-146 single-work catalog
	// claim (whose subject — a status transition on a wiki row — can no longer
	// occur). The nightly reconcile that was the claim's slower twin was
	// unregistered in the same wave.
	//
	// Residual edit_* rows still carrying entity_type='galgame.game' after the
	// rekey are the ~36 unanchorable residue rows, which T3 deletes. They are
	// unrenderable in the meantime, which is correct: every face that could
	// have rendered them is gone.
	editEngine := editing.NewEngine(catalogDB.DB(), editRegistry)
	// Per-family perm resolvers (E3a ruling 1): the generic edit face routes
	// an asserted actor's roles through the vocabulary of the entity's own
	// family — registered here alongside the EntityTypeSpecs, so the face
	// hardcodes no family name and the engine stays family-agnostic.
	catHandler.SetupEdit(s2sAPI, editEngine, catHandler.PermResolvers{
		"catalog": catalogPerm.Resolver,
	})
	// The claim lifecycle (wave 155 W2/W3) rides the same S2S API and the same
	// asserted-actor convention. It is registered AFTER the engine exists
	// because the revision feed is a read-only projection of the engine's log.
	catHandler.SetupLifecycle(s2sAPI, claimSvc, editEngine, catHandler.PermResolvers{
		"catalog": catalogPerm.Resolver,
	})
	// The best-cover vote face (wave 175): two advisory ops on the same S2S API
	// and the same asserted-actor convention. It writes catalog_cover_vote and
	// nothing else — the editorial cover columns are not its to move.
	catHandler.SetupCoverVotes(s2sAPI, service.NewCoverVoteService(catalogDB.DB()))

	// NextMoe open API: serve the frozen public spec unauthenticated at its face
	// root — the machine-readable contract itself must not need a key. Built ONCE
	// at boot through the same spec-only Setup function cmd/gen-openapi uses, so
	// the served JSON always matches the frozen Tier-A YAML the CI freeze gates
	// pin. Registered BEFORE the /v1 groups below: an exact GET route outranks
	// their prefix middleware, so this path bypasses the devapi key chain while
	// everything else under /v1 stays keyed.
	//
	// Its sibling /v1/galgame/openapi.json retired with the galgame public face
	// at wave 146 (2026-07-30) — the path now falls through to that face's 410
	// catch-all, which is the honest answer for a decommissioned contract.
	catalogSpec, err := json.Marshal(catHandler.SetupCatalogPublicSpec(fiber.New()).OpenAPI())
	if err != nil {
		slog.Error("marshal catalog public spec", "error", err)
		os.Exit(1)
	}
	application.Fiber.Get("/v1/catalog/openapi.json", func(c fiber.Ctx) error {
		c.Set("Content-Type", "application/json")
		c.Set("Cache-Control", "public, max-age=3600")
		return c.Send(catalogSpec)
	})

	// NextMoe open API: catalog public projection (/v1/catalog/*). A NEW public
	// read-only bypass (step 03) behind the shared devapi middleware chain; the
	// S2S (/api/v1/catalog) and admin (/api/v1/admin/catalog) faces are untouched
	// (disjoint prefixes). Probable anchors and r18 works never surface.
	setupPublicCatalog(application, cfg, catalogDB, readSvc, resolveSvc, searcher, statsSvc)

	// What is left of the galgame HTTP surface: the /v1/galgame 410 tombstone.
	//
	// This process hosted the whole surface from the wiki-retirement W2 merge
	// until wave 161's N5 window — the /api staff face (admin/ban + the
	// tag/official/engine/series CRUD family + the staff catalog browser), the
	// devapi-gated /internal platform-workflow / user-write / proposal faces,
	// and the two S2S cron feeds. All of them read and wrote the galgame table
	// family this window DROPs, so they retire in the same deploy that stops
	// the wiki from minting new rows (§3 SW-E1: this must land BEFORE the
	// edit-history rekey, or the write face keeps producing galgame.game rows
	// behind it).
	//
	// The 410 stays (wave 146 ruling) and needs nothing: no pool, no search
	// client, no engine, no credential. Its prefix is disjoint from the catalog
	// faces (/api/v1/catalog, /api/v1/admin/catalog, /v1/catalog), and the
	// shared global middleware + /healthz are already installed above.
	galgameapp.MountRetiredPublic(application)

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

	// Permission overlay (docs/auth/04 §7). This service enforces its own
	// domain's keys, and those keys can be widened at runtime by the permission
	// console, so it must keep its Resolver current. It reads the overlay
	// straight from the main database it already holds a connection to; with no
	// Redis in this process the refresh runs on the poll interval, which is the
	// floor that makes the overlay reliable everywhere.
	permCtx, cancelPerm := context.WithCancel(context.Background())
	defer cancelPerm()
	permissions.NewDistributor(application.DB.DB(), permissions.Live(), nil).Start(permCtx)

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
	statsSvc *service.StatsService,
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
	var imgCli *imageclient.Client
	if cfg.ImageClient.ClientID != "" && cfg.ImageClient.ClientSecret != "" {
		imgCli = imageclient.New(imageclient.Config{
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
	// Edit-face byte upload (wave 169): the multipart leg the row-set editors
	// (covers/screenshots) obtain their hashes from. Deliberately NOT imgCli:
	// that identity is the wiki-era client the meta reads still ride, while
	// new bytes must land under the catalog SITE's own client — the scope the
	// daily catalog refping keeps out of the image GC. Unset credentials
	// disable the leg (503) rather than silently falling back to the wrong
	// site, which would strand every editor upload outside the refping sweep.
	var editUpload catHandler.EditImageUpload
	if cfg.CatalogImageClient.ClientID != "" && cfg.CatalogImageClient.ClientSecret != "" {
		editUpload = imageclient.New(imageclient.Config{
			BaseURL:      cfg.CatalogImageClient.BaseURL,
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     cfg.CatalogImageClient.ClientID,
			ClientSecret: cfg.CatalogImageClient.ClientSecret,
		}).UploadWithSub
	} else {
		slog.Warn("catalog edit face: catalog image client not configured — editor image upload disabled (503)")
	}
	catHandler.SetupEditImages(application.Fiber, editUpload)
	// The works product search (A2-1d) runs its filters/facets/sort inside the
	// same catalog_works index the entity autocomplete uses, then re-hydrates
	// the page from Postgres.
	publicSvc.WithWorksSearch(searcher)
	publicH := catHandler.NewPublicHandler(publicSvc, resolveSvc, searcher, statsSvc)

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
	// before the /works/:id catch-all. /works/search is the product search face
	// (A2-1d) and must also precede /works/:id, or "search" would be parsed as
	// an id.
	v1.Get("/works", publicH.WorksList)
	v1.Get("/works/search", publicH.WorksSearch)
	v1.Get("/changes", publicH.Changes)
	// Slim public counts (149b) — the product-facing "how big is this
	// catalogue" number; the internal dashboard stays on the S2S face.
	v1.Get("/stats", publicH.Stats)
	// Release-grain new-releases timeline (wave 174) — the calendar's sibling
	// one grain down, where ports and re-editions are finally visible.
	v1.Get("/releases", publicH.Releases)
	// Release-calendar buckets (A2-1c): month view + the two pending buckets.
	v1.Get("/calendar", publicH.Calendar)
	v1.Get("/calendar/pending", publicH.CalendarPending)
	v1.Get("/calendar/tba", publicH.CalendarTBA)
	// Taxonomy browse lanes (A2-1b), each registered before its own /:id.
	v1.Get("/labels", publicH.LabelsList)
	v1.Get("/tags", publicH.TagsList)
	v1.Get("/engines", publicH.EnginesList)
	v1.Get("/series", publicH.SeriesList)
	v1.Get("/works/:id", publicH.WorkDetail)
	v1.Get("/names/:id", publicH.Name)
	v1.Get("/characters/:id", publicH.Character)
	v1.Get("/labels/:id", publicH.Label)
	v1.Get("/tags/:id", publicH.Tag)
	v1.Get("/engines/:id", publicH.EngineDetail)
	// Series detail (149c) — the address of the grouping entity works?series_id=
	// filters on. Its browse lane sits above with the other three.
	v1.Get("/series/:id", publicH.Series)

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
