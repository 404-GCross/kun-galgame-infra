package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	"api/pkg/config"
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
	// OAuth client repo lives on the OAuth DB (read-only from galgame service);
	// used to authenticate Basic-Auth cron callers on the message feed.
	oauthClientRepo := siteRepo.NewOAuthClientRepository(oauthDB)

	// Services
	galgameSvc := galgameService.NewGalgameService(galgameRepository, revisionRepo, prRepo, userReadRepo)
	submissionSvc := galgameService.NewSubmissionService(galgameRepository, messageRepo)
	messageSvc := galgameService.NewMessageService(messageRepo, galgameRepository, userReadRepo)
	adminSvc := galgameService.NewAdminService(galgameRepository, messageRepo)

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
	} else {
		slog.Warn("image client not configured; multipart banner uploads in galgame Create/Update/PR will be rejected")
	}

	// Handlers
	galgameH := galgameHandler.NewGalgameHandler(galgameSvc, searchHook, imgCli)
	revisionH := galgameHandler.NewRevisionHandler(galgameSvc, imgCli)
	linkH := galgameHandler.NewLinkHandler(galgameSvc, galgameRepository)
	contributorH := galgameHandler.NewContributorHandler(galgameRepository, userReadRepo)
	tagH := galgameHandler.NewTagHandler(tagRepo, searchHook)
	officialH := galgameHandler.NewOfficialHandler(officialRepo, searchHook)
	engineH := galgameHandler.NewEngineHandler(engineRepo)
	seriesH := galgameHandler.NewSeriesHandler(seriesRepo)
	adminH := galgameHandler.NewAdminHandler(adminRepo, adminSvc, searchHook)
	submissionH := galgameHandler.NewSubmissionHandler(submissionSvc, searchHook, imgCli)
	messageH := galgameHandler.NewMessageHandler(messageSvc)
	searchH := galgameHandler.NewSearchHandler(searchSvc)

	// JWT auth middleware
	jwtAuth := middleware.JWTAuth(cfg.JWT.Secret)
	// OptionalJWT — populates user_uid when a valid Bearer token is present,
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
	galgame.Get("/user/:uid/stats", galgameH.UserStats)
	galgame.Get("/:gid", galgameH.Get)
	galgame.Get("/:gid/revisions", revisionH.ListRevisions)
	galgame.Get("/:gid/revisions/:rev", revisionH.GetRevision)
	galgame.Get("/:gid/revisions/:rev/diff", revisionH.GetRevisionDiff)
	galgame.Get("/:gid/prs", revisionH.ListPRs)
	galgame.Get("/:gid/prs/:id", revisionH.GetPR)
	galgame.Get("/:gid/links", linkH.ListLinks)
	galgame.Get("/:gid/aliases", linkH.ListAliases)
	galgame.Get("/:gid/contributors", contributorH.List)

	// Authenticated routes
	galgameAuth := galgame.Group("", jwtAuth)
	galgameAuth.Post("/", galgameH.Create)
	galgameAuth.Put("/:gid", galgameH.Update)
	galgameAuth.Post("/:gid/revert", revisionH.Revert)
	galgameAuth.Post("/:gid/prs", revisionH.SubmitPR)
	galgameAuth.Put("/:gid/prs/:id/merge", revisionH.MergePR)
	galgameAuth.Put("/:gid/prs/:id/decline", revisionH.DeclinePR)
	galgameAuth.Post("/:gid/links", linkH.CreateLink)
	galgameAuth.Delete("/:gid/links", linkH.DeleteLink)
	galgameAuth.Post("/:gid/aliases", linkH.CreateAlias)
	galgameAuth.Delete("/:gid/aliases", linkH.DeleteAlias)
	galgameAuth.Delete("/:gid/contributors/:uid", contributorH.Delete)

	// ── User submission flow ──
	// Static-path routes (submit / mine / messages/*) must be registered
	// BEFORE the /:gid param route in galgameAuth above. Fiber matches the
	// first declared route that fits, so registering them via the group's
	// own .Post/.Get keeps them static-first as long as we add them here
	// (which compiles to the same group at server startup).
	galgameAuth.Post("/submit", submissionH.Submit)
	galgameAuth.Get("/mine", submissionH.ListMine)
	galgameAuth.Post("/:gid/claim", submissionH.Claim)
	galgameAuth.Patch("/:gid", submissionH.PatchDraft)
	galgameAuth.Delete("/:gid", submissionH.DeleteDraft)

	// ── Messages ──
	// /messages/mine — end-user JWT
	galgameAuth.Get("/messages/mine", messageH.ListMine)
	// /messages/feed — service-to-service Basic Auth (kungal/moyu cron).
	// Registered directly under api, NOT galgameAuth, so it uses Basic Auth
	// instead of Bearer JWT.
	galgame.Get("/messages/feed",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		messageH.ListFeed,
	)

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

	// ── Tag ──
	tag := api.Group("/tag")
	tag.Get("/", tagH.List)
	tag.Get("/search", searchH.Tag) // Meilisearch-backed (replaces DB LIKE search)
	tag.Get("/multi", tagH.Multi)
	tag.Get("/:name", tagH.GetByName)
	tag.Put("/", jwtAuth, tagH.Update)

	// ── Official ──
	official := api.Group("/official")
	official.Get("/", officialH.List)
	official.Get("/search", searchH.Official) // Meilisearch-backed
	official.Get("/:name", officialH.GetByName)
	official.Put("/", jwtAuth, officialH.Update)

	// ── Engine ──
	engine := api.Group("/engine")
	engine.Get("/", engineH.List)
	engine.Get("/:name", engineH.GetByName)
	engine.Put("/", jwtAuth, engineH.Update)

	// ── Series ──
	series := api.Group("/series")
	series.Get("/", seriesH.List)
	series.Get("/search", seriesH.Search)
	series.Get("/:id", seriesH.Get)
	seriesAuth := series.Group("", jwtAuth)
	seriesAuth.Post("/", seriesH.Create)
	seriesAuth.Post("/modal", seriesH.Modal)
	seriesAuth.Put("/:id", seriesH.Update)
	seriesAuth.Delete("/:id", seriesH.Delete)
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
