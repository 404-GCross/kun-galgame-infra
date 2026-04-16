package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/logger"

	galgameHandler "api/internal/platform/galgame/handler"
	galgameRepo "api/internal/platform/galgame/repository"
	galgameService "api/internal/platform/galgame/service"

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

	setupRoutes(application, cfg, wikiDB)

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

func setupRoutes(a *app.App, cfg *config.Config, wikiDB *database.PostgresDB) {
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

	// Services
	galgameSvc := galgameService.NewGalgameService(galgameRepository, revisionRepo, prRepo, userReadRepo)

	// Handlers
	galgameH := galgameHandler.NewGalgameHandler(galgameSvc)
	revisionH := galgameHandler.NewRevisionHandler(galgameSvc)
	linkH := galgameHandler.NewLinkHandler(galgameSvc, galgameRepository)
	contributorH := galgameHandler.NewContributorHandler(galgameRepository, userReadRepo)
	tagH := galgameHandler.NewTagHandler(tagRepo)
	officialH := galgameHandler.NewOfficialHandler(officialRepo)
	engineH := galgameHandler.NewEngineHandler(engineRepo)
	seriesH := galgameHandler.NewSeriesHandler(seriesRepo)

	// JWT auth middleware
	jwtAuth := middleware.JWTAuth(cfg.JWT.Secret)

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())
	a.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// API routes
	api := a.Fiber.Group("/api")

	api.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// ── Galgame CRUD ──
	galgame := api.Group("/galgame")
	galgame.Get("/", galgameH.List)
	galgame.Get("/batch", galgameH.BatchGet)
	galgame.Get("/check", galgameH.CheckVNDB)
	galgame.Get("/:gid", galgameH.Get)
	galgameAuth := galgame.Group("", jwtAuth)
	galgameAuth.Post("/", galgameH.Create)
	galgameAuth.Put("/:gid", galgameH.Update)

	// ── Revisions ──
	galgame.Get("/:gid/revisions", revisionH.ListRevisions)
	galgame.Get("/:gid/revisions/:rev", revisionH.GetRevision)
	galgame.Get("/:gid/revisions/:rev/diff", revisionH.GetRevisionDiff)
	galgameAuth.Post("/:gid/revert", revisionH.Revert)

	// ── PRs ──
	galgame.Get("/:gid/prs", revisionH.ListPRs)
	galgame.Get("/:gid/prs/:id", revisionH.GetPR)
	galgameAuth.Post("/:gid/prs", revisionH.SubmitPR)
	galgameAuth.Put("/:gid/prs/:id/merge", revisionH.MergePR)
	galgameAuth.Put("/:gid/prs/:id/decline", revisionH.DeclinePR)

	// ── Links ──
	galgame.Get("/:gid/links", linkH.ListLinks)
	galgameAuth.Post("/:gid/links", linkH.CreateLink)
	galgameAuth.Delete("/:gid/links", linkH.DeleteLink)

	// ── Aliases ──
	galgame.Get("/:gid/aliases", linkH.ListAliases)
	galgameAuth.Post("/:gid/aliases", linkH.CreateAlias)
	galgameAuth.Delete("/:gid/aliases", linkH.DeleteAlias)

	// ── Contributors ──
	galgame.Get("/:gid/contributors", contributorH.List)
	galgameAuth.Delete("/:gid/contributors/:uid", contributorH.Delete)

	// ── Tag ──
	tag := api.Group("/tag")
	tag.Get("/", tagH.List)
	tag.Get("/search", tagH.Search)
	tag.Get("/multi", tagH.Multi)
	tag.Get("/:name", tagH.GetByName)
	tag.Put("/", jwtAuth, tagH.Update)

	// ── Official ──
	official := api.Group("/official")
	official.Get("/", officialH.List)
	official.Get("/search", officialH.Search)
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
