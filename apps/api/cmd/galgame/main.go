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

	// Services
	galgameSvc := galgameService.NewGalgameService(galgameRepository, userReadRepo)

	// Handlers
	galgameH := galgameHandler.NewGalgameHandler(galgameSvc)

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())
	a.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	// API routes
	api := a.Fiber.Group("/api")

	api.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Public galgame routes
	galgame := api.Group("/galgame")
	galgame.Get("/", galgameH.List)
	galgame.Get("/check", galgameH.CheckVNDB) // Must be before /:gid
	galgame.Get("/:gid", galgameH.Get)

	// Protected galgame routes
	galgameAuth := galgame.Group("", middleware.JWTAuth(cfg.JWT.Secret))
	galgameAuth.Post("/", galgameH.Create)
	galgameAuth.Put("/:gid", galgameH.Update)
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
