package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/galgameapp"
	"api/internal/infrastructure/database"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

// cmd/galgame is a thin shell around galgameapp.Mount: it owns the process-level
// concerns (config, the galgame content DB connection, the Meili client, the
// shared global middleware, /healthz, and the server lifecycle) and then mounts
// the galgame HTTP surface. The same Mount is called by cmd/catalog after the
// wiki-retirement W2 merge, so both processes serve the galgame surface from one
// source of wiring.
func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// `healthcheck` subcommand for container HEALTHCHECK (distroless has no
	// shell/curl). No-op for a normal start; exits before any infra is touched.
	health.MaybeProbe(getPort("KUN_GALGAME_PORT", 9280), "/healthz")

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

	// Second DB connection for galgame content database (kun_galgame_wiki
	// pre-W1, kun_catalog after — driven by KUN_GALGAME_PG_DATABASE).
	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to galgame content database", "error", err)
		os.Exit(1)
	}

	// Meilisearch client (galgame index self-heal happens inside Mount).
	searchClient, err := searchInfra.NewClient(cfg.Meilisearch)
	if err != nil {
		slog.Error("failed to init meilisearch client", "error", err)
		os.Exit(1)
	}

	// Global middleware — shared, process-level concerns. Kept here (not in
	// Mount) so a process that co-hosts galgame with another surface does not
	// double-register them.
	application.Fiber.Use(middleware.RequestID())
	application.Fiber.Use(middleware.Logger())
	// Liveness probe — root /healthz, before CORS, used by container HEALTHCHECK.
	application.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})
	application.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))

	galgameapp.Mount(application, cfg, galgameapp.Deps{
		OAuthDB:   application.DB.DB(),
		GalgameDB: wikiDB.DB(),
		Search:    searchClient,
	})

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
