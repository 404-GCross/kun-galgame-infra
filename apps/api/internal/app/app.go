package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"api/internal/infrastructure/cache"
	"api/internal/infrastructure/database"
	"api/pkg/config"

	"github.com/gofiber/fiber/v3"
)

// App represents the application
type App struct {
	config *config.Config
	fiber  *fiber.App
	db     *database.PostgresDB
	cache  *cache.RedisCache
}

// New creates a new application
func New(cfg *config.Config) (*App, error) {
	app := &App{
		config: cfg,
	}

	// Initialize database
	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	app.db = db

	// Initialize cache
	redisCache, err := cache.NewRedisCache(cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}
	app.cache = redisCache

	// Initialize Fiber
	app.fiber = fiber.New(fiber.Config{
		AppName:      "kun-oauth-admin",
		ServerHeader: "kun-oauth-admin",
		ErrorHandler: app.errorHandler,
	})

	// Setup routes
	app.setupRoutes()

	return app, nil
}

// Run starts the application
func (a *App) Run() error {
	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start server in goroutine
	go func() {
		addr := fmt.Sprintf("%s:%d", a.config.Server.Host, a.config.Server.Port)
		slog.Info("starting server", "addr", addr)
		if err := a.fiber.Listen(addr); err != nil {
			slog.Error("server error", "error", err)
		}
	}()

	// Wait for interrupt
	<-ctx.Done()
	slog.Info("shutting down server...")

	// Shutdown
	if err := a.fiber.Shutdown(); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	// Close database
	if err := a.db.Close(); err != nil {
		slog.Error("failed to close database", "error", err)
	}

	// Close cache
	if err := a.cache.Close(); err != nil {
		slog.Error("failed to close cache", "error", err)
	}

	slog.Info("server stopped")
	return nil
}

// errorHandler handles Fiber errors
func (a *App) errorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}

	return c.Status(code).JSON(fiber.Map{
		"code":    code,
		"message": message,
	})
}

// DB returns the database instance
func (a *App) DB() *database.PostgresDB {
	return a.db
}

// Cache returns the cache instance
func (a *App) Cache() *cache.RedisCache {
	return a.cache
}

// Fiber returns the Fiber app instance
func (a *App) Fiber() *fiber.App {
	return a.fiber
}
