package main

import (
	"context"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/logger"

	galgameHandler "api/internal/platform/galgame/handler"
	galgameRepo "api/internal/platform/galgame/repository"
	galgameSearch "api/internal/platform/galgame/search"
	galgameService "api/internal/platform/galgame/service"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/imageclient"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// `healthcheck` subcommand for container HEALTHCHECK (distroless has no
	// shell/curl). No-op for a normal start; exits before any infra is touched.
	health.MaybeProbe(getPort("KUN_GALGAME_PORT", 9280), "/api/health")

	logger.Init(cfg.Server.Env)

	// Primary app with OAuth DB (for user lookups via read-only connection)
	application, err := app.New(cfg, app.Options{
		Name:      "kun-galgame",
		NeedCache: false,
	})
	if err != nil {
		slog.Error("failed to create application", "error", err)
		os.Exit(1)
	}

	// Second DB connection for galgame wiki database
	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to galgame wiki database", "error", err)
		os.Exit(1)
	}

	// Meilisearch client + index settings self-heal
	searchClient, err := searchInfra.NewClient(cfg.Meilisearch)
	if err != nil {
		slog.Error("failed to init meilisearch client", "error", err)
		os.Exit(1)
	}
	if err := galgameSearch.EnsureIndexes(searchClient); err != nil {
		// Non-fatal: search endpoints will fail but DB-backed routes still work.
		// Bulk reindex script (cmd/reindex-search) is the recovery path.
		slog.Warn("EnsureIndexes failed — search endpoints may not work until fixed", "error", err)
	} else {
		// Indexes exist & settings are right — but they might be empty
		// (fresh Meilisearch instance, lost data.ms, etc). Surface a
		// loud warning so operators know to run cmd/reindex-search.
		galgameSearch.WarnIfIndexesEmpty(searchClient)
	}

	setupRoutes(application, cfg, wikiDB, searchClient)

	port := getPort("KUN_GALGAME_PORT", 9280)
	if err := application.Run(cfg.Server.Host, port); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}

	// Close wiki DB on shutdown
	if err := wikiDB.Close(); err != nil {
		slog.Error("failed to close wiki database", "error", err)
	}
}

func setupRoutes(a *app.App, cfg *config.Config, wikiDB *database.PostgresDB, searchClient *searchInfra.Client) {
	oauthDB := a.DB.DB() // kun_oauth_admin — read-only for user info
	wiki := wikiDB.DB()  // kun_galgame_wiki — read-write

	// Repositories
	galgameRepository := galgameRepo.NewGalgameRepository(wiki)
	userReadRepo := galgameRepo.NewUserReadonlyRepository(oauthDB)
	revisionRepo := galgameRepo.NewRevisionRepository(wiki)
	prRepo := galgameRepo.NewPRRepository(wiki)
	tagRepo := galgameRepo.NewTagRepository(wiki)
	officialRepo := galgameRepo.NewOfficialRepository(wiki)
	engineRepo := galgameRepo.NewEngineRepository(wiki)
	seriesRepo := galgameRepo.NewSeriesRepository(wiki)
	adminRepo := galgameRepo.NewAdminRepository(wiki)
	messageRepo := galgameRepo.NewMessageRepository(wiki)
	taxRevRepo := galgameRepo.NewTaxonomyRevisionRepository(wiki)
	// OAuth client repo lives on the OAuth DB (read-only from galgame service);
	// used to authenticate Basic-Auth cron callers on the message feed.
	oauthClientRepo := siteRepo.NewOAuthClientRepository(oauthDB)

	// Services
	galgameSvc := galgameService.NewGalgameService(galgameRepository, revisionRepo, prRepo, userReadRepo)
	submissionSvc := galgameService.NewSubmissionService(galgameRepository, messageRepo)
	messageSvc := galgameService.NewMessageService(messageRepo, galgameRepository, userReadRepo)
	adminSvc := galgameService.NewAdminService(galgameRepository, messageRepo)
	// TaxonomyService — orchestrates tag/official/engine/series CRUD via
	// the polymorphic taxonomy_revision audit. Every mutating handler
	// path below routes through it.
	taxSvc := galgameService.NewTaxonomyService(tagRepo, officialRepo, engineRepo, seriesRepo, taxRevRepo, galgameRepository)

	// Meilisearch: indexer + write-through hook + search service
	indexer := galgameSearch.NewIndexer(searchClient)
	searchHook := galgameSearch.NewHook(wiki, indexer)
	searchSvc := galgameSearch.NewService(searchClient)

	// Image client (singleton, optional). Used by Galgame Create/Update +
	// PR submit when caller sends multipart with a banner file.
	// Nil if KUN_IMAGE_CLIENT_ID/SECRET unset → multipart with file 400s
	// with a clear error; JSON-only callers unaffected.
	var imgCli *imageclient.Client
	if cfg.ImageClient.ClientID != "" && cfg.ImageClient.ClientSecret != "" {
		imgCli = imageclient.New(imageclient.Config{
			BaseURL:      cfg.ImageClient.BaseURL,
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     cfg.ImageClient.ClientID,
			ClientSecret: cfg.ImageClient.ClientSecret,
		})
		slog.Info("image client configured", "base_url", cfg.ImageClient.BaseURL)

		// Wire the existence probe into the galgame service: Revert uses
		// it to detect snapshots that point at TTL-deleted image hashes.
		// Implementation reuses ReferencePing — which both probes
		// existence (returns NotFound) AND refreshes the TTL for the
		// hashes that *do* exist, so revert is also a free "ref-touch"
		// for everything in the reverted snapshot.
		galgameSvc.WithImageProbe(func(ctx context.Context, hashes []string) ([]string, error) {
			res, err := imgCli.ReferencePing(ctx, hashes)
			if err != nil {
				return nil, err
			}
			return res.NotFound, nil
		})
	} else {
		slog.Warn("image client not configured; multipart banner uploads in galgame Create/Update/PR will be rejected; Revert will skip image-existence probe")
	}

	// Handlers
	galgameH := galgameHandler.NewGalgameHandler(galgameSvc, searchHook, imgCli)
	revisionH := galgameHandler.NewRevisionHandler(galgameSvc, imgCli)
	linkH := galgameHandler.NewLinkHandler(galgameSvc, galgameRepository)
	contributorH := galgameHandler.NewContributorHandler(galgameRepository, userReadRepo)
	tagH := galgameHandler.NewTagHandler(tagRepo, taxSvc, searchHook)
	officialH := galgameHandler.NewOfficialHandler(officialRepo, taxSvc, searchHook)
	engineH := galgameHandler.NewEngineHandler(engineRepo, taxSvc)
	seriesH := galgameHandler.NewSeriesHandler(seriesRepo, taxSvc)
	// One handler covers ListRevisions / GetRevision / Revert for all
	// four taxonomy entities — the entity discriminator is baked into
	// the route wrapper methods (TagListRevisions / OfficialRevert / …).
	taxRevH := galgameHandler.NewTaxonomyRevisionHandler(taxSvc)
	adminH := galgameHandler.NewAdminHandler(adminRepo, adminSvc, searchHook)
	submissionH := galgameHandler.NewSubmissionHandler(submissionSvc, searchHook, imgCli)
	messageH := galgameHandler.NewMessageHandler(messageSvc)
	searchH := galgameHandler.NewSearchHandler(searchSvc)

	// JWT auth middleware
	jwtAuth := middleware.JWTAuth(cfg.JWT.Secret)
	// OptionalJWT — populates user_id when a valid Bearer token is present,
	// but never blocks the request. Used on /galgame/batch and /galgame/search
	// so anonymous callers still get status=0-only results while authenticated
	// ones additionally see their own pending/declined drafts.
	optionalJWT := middleware.OptionalJWT(cfg.JWT.Secret)

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())
	a.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// API routes
	api := a.Fiber.Group("/api")

	api.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// ── Galgame ──
	galgame := api.Group("/galgame")

	// Public GET routes (must be registered before auth group)
	galgame.Get("/", galgameH.List)
	// search & batch accept an optional Bearer JWT so authenticated callers
	// can also see their own pending/declined drafts (include_pending=true
	// for search; automatic for batch).
	galgame.Get("/search", optionalJWT, searchH.Galgame)
	galgame.Get("/batch", optionalJWT, galgameH.BatchGet)
	galgame.Get("/check", galgameH.CheckVNDB)
	galgame.Get("/user/:id/stats", galgameH.UserStats)
	// GET /mine MUST be registered before the /:gid catch-all: both are
	// GET and Fiber matches by registration order, so a /:gid registered
	// first binds :gid="mine" and the handler ParseInt-fails with
	// {"code":2,"无效的 ID"}. It needs auth, so attach jwtAuth inline
	// (same middleware the galgameAuth group uses) rather than relying on
	// the later group. submit/claim/patch/delete on /:gid are POST/PATCH/
	// DELETE so they don't collide and stay in the auth group below.
	galgame.Get("/mine", jwtAuth, submissionH.ListMine)
	galgame.Get("/:gid", optionalJWT, galgameH.Get)
	// optionalJWT so the visibility gate (AssertGalgameVisible) can let an
	// authenticated submitter see their own pending/declined draft's history,
	// while anonymous callers only ever see published galgames' revisions/PRs.
	galgame.Get("/:gid/revisions", optionalJWT, revisionH.ListRevisions)
	galgame.Get("/:gid/revisions/:rev", optionalJWT, revisionH.GetRevision)
	galgame.Get("/:gid/revisions/:rev/diff", optionalJWT, revisionH.GetRevisionDiff)
	galgame.Get("/:gid/prs", optionalJWT, revisionH.ListPRs)
	galgame.Get("/:gid/prs/:id", optionalJWT, revisionH.GetPR)
	galgame.Get("/:gid/links", linkH.ListLinks)
	galgame.Get("/:gid/aliases", linkH.ListAliases)
	galgame.Get("/:gid/contributors", contributorH.List)

	// ── Cross-service endpoints (OAuth Client Basic Auth) ──
	//
	// MUST be registered BEFORE the `galgameAuth := galgame.Group("", jwtAuth)`
	// line below. Fiber v3's `Group("", middleware)` with an empty prefix is
	// equivalent to `app.Use("/galgame", middleware)` (see fiber/v3/group.go
	// Group ctor: it calls `app.register(methodUse, prefix, …)` when the group
	// has handlers). Use() then matches every `/galgame/*` route registered
	// AFTER it — including ones registered on the parent `galgame` group
	// without `galgameAuth`. So any endpoint that needs non-Bearer auth
	// (Basic / public) MUST land above this fence; otherwise jwtAuth runs
	// first and rejects Basic with code=10002.
	//
	// /messages/feed: kungal/moyu cron pulls wiki messages here via Basic Auth.
	galgame.Get("/messages/feed",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		messageH.ListFeed,
	)

	// ─── Bearer JWT fence — every route below this point inherits jwtAuth ───
	galgameAuth := galgame.Group("", jwtAuth)
	// POST /galgame is the admin direct-publish bypass: it creates a
	// status=0 entry immediately, skipping the user submission queue. Regular
	// users must go through POST /galgame/submit (creates status=3, awaits
	// review). Locked to admin/moderator so non-staff can't bypass review.
	galgameAuth.Post("/", middleware.RequireRole("admin", "moderator"), galgameH.Create)
	galgameAuth.Put("/:gid", galgameH.Update)
	galgameAuth.Post("/:gid/revert", revisionH.Revert)
	galgameAuth.Post("/:gid/prs", revisionH.SubmitPR)
	galgameAuth.Put("/:gid/prs/:id/merge", revisionH.MergePR)
	galgameAuth.Put("/:gid/prs/:id/decline", revisionH.DeclinePR)
	galgameAuth.Post("/:gid/links", linkH.CreateLink)
	galgameAuth.Delete("/:gid/links", linkH.DeleteLink)
	galgameAuth.Post("/:gid/aliases", linkH.CreateAlias)
	galgameAuth.Delete("/:gid/aliases", linkH.DeleteAlias)
	galgameAuth.Delete("/:gid/contributors/:id", contributorH.Delete)

	// ── User submission flow ──
	// GET /mine is registered earlier (before the public /:gid catch-all)
	// — see the comment there. submit/claim are POST and patch/delete are
	// PATCH/DELETE, none of which collide with GET /:gid, so they stay
	// here in the auth group.
	galgameAuth.Post("/submit", submissionH.Submit)
	galgameAuth.Post("/:gid/claim", submissionH.Claim)
	galgameAuth.Patch("/:gid", submissionH.PatchDraft)
	galgameAuth.Delete("/:gid", submissionH.DeleteDraft)

	// ── Messages ──
	// /messages/mine — end-user JWT.
	// /messages/feed is registered ABOVE the jwtAuth fence; see comment there.
	galgameAuth.Get("/messages/mine", messageH.ListMine)

	// ── Admin ──
	// Admin endpoints require both JWT validity AND admin/moderator role —
	// without the role check anyone with a valid OAuth access_token could
	// modify galgame status.
	admin := api.Group("/admin", jwtAuth, middleware.RequireRole("admin", "moderator"))
	admin.Get("/stats", adminH.Stats)
	admin.Get("/galgame", adminH.ListGalgames)
	admin.Get("/galgame/messages", messageH.ListAdminQueue)
	admin.Get("/galgame/:gid", adminH.GetGalgame)
	admin.Put("/galgame/:gid/status", adminH.UpdateGalgameStatus)
	// Bulk soft-delete all of a user's galgame (severe-spam cleanup;
	// content-side companion to the OAuth anonymize action).
	admin.Post("/galgame/ban-by-user/:userId", adminH.BanGalgamesByUser)

	// ── Tag ──
	// Create: any logged-in user (introduce a tag for original/doujin
	// works missing from VNDB). Update/Delete: admin/moderator (role
	// checked inside the handler, same as series).
	tag := api.Group("/tag")
	tag.Get("/", tagH.List)
	tag.Get("/search", searchH.Tag) // Meilisearch-backed (replaces DB LIKE search)
	tag.Get("/multi", tagH.Multi)
	tag.Get("/:name", tagH.GetByName)
	tag.Post("/", jwtAuth, tagH.Create)
	tag.Put("/", jwtAuth, tagH.Update)
	tag.Delete("/:id", jwtAuth, tagH.Delete)
	// Tag revision/revert (admin/moderator) — same surface for each of
	// the four taxonomy entities; see TaxonomyRevisionHandler.
	tag.Get("/:id/revisions", taxRevH.TagListRevisions)
	tag.Get("/:id/revisions/:rev", taxRevH.TagGetRevision)
	tag.Post("/:id/revert", jwtAuth, taxRevH.TagRevert)

	// ── Official ──
	official := api.Group("/official")
	official.Get("/", officialH.List)
	official.Get("/search", searchH.Official) // Meilisearch-backed
	official.Get("/:name", officialH.GetByName)
	official.Post("/", jwtAuth, officialH.Create)
	official.Put("/", jwtAuth, officialH.Update)
	official.Delete("/:id", jwtAuth, officialH.Delete)
	official.Get("/:id/revisions", taxRevH.OfficialListRevisions)
	official.Get("/:id/revisions/:rev", taxRevH.OfficialGetRevision)
	official.Post("/:id/revert", jwtAuth, taxRevH.OfficialRevert)

	// ── Engine ──
	engine := api.Group("/engine")
	engine.Get("/", engineH.List)
	engine.Get("/:name", engineH.GetByName)
	engine.Post("/", jwtAuth, engineH.Create)
	engine.Put("/", jwtAuth, engineH.Update)
	engine.Delete("/:id", jwtAuth, engineH.Delete)
	engine.Get("/:id/revisions", taxRevH.EngineListRevisions)
	engine.Get("/:id/revisions/:rev", taxRevH.EngineGetRevision)
	engine.Post("/:id/revert", jwtAuth, taxRevH.EngineRevert)

	// ── Series ──
	series := api.Group("/series")
	series.Get("/", seriesH.List)
	series.Get("/search", seriesH.Search)
	series.Get("/:id", seriesH.Get)
	series.Get("/:id/revisions", taxRevH.SeriesListRevisions)
	series.Get("/:id/revisions/:rev", taxRevH.SeriesGetRevision)
	seriesAuth := series.Group("", jwtAuth)
	seriesAuth.Post("/", seriesH.Create)
	seriesAuth.Post("/modal", seriesH.Modal)
	seriesAuth.Put("/:id", seriesH.Update)
	seriesAuth.Delete("/:id", seriesH.Delete)
	seriesAuth.Post("/:id/revert", taxRevH.SeriesRevert)
}

func getPort(envKey string, defaultPort int) int {
	if v := os.Getenv(envKey); v != "" {
		p := 0
		for _, c := range v {
			p = p*10 + int(c-'0')
		}
		if p > 0 {
			return p
		}
	}
	return defaultPort
}
