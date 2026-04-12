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

// App represents a service application with shared infrastructure.
// Each cmd/ entry creates an App, configures routes, then calls Run().
type App struct {
	Config *config.Config
	Fiber  *fiber.App
	DB     *database.PostgresDB
	Cache  *cache.RedisCache
}

// Options controls which infrastructure to initialize
type Options struct {
	Name      string // Service name (e.g., "oauth", "artifact")
	NeedCache bool   // Whether to initialize Redis
}

// New creates a new application with the specified infrastructure
func New(cfg *config.Config, opts Options) (*App, error) {
	a := &App{Config: cfg}

	// Initialize database (always needed)
	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	a.DB = db

	// Initialize cache (optional)
	if opts.NeedCache {
		redisCache, err := cache.NewRedisCache(cfg.Redis)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to redis: %w", err)
		}
		a.Cache = redisCache
	}

	// Initialize Fiber
	name := opts.Name
	if name == "" {
		name = "kun-api"
	}
	a.Fiber = fiber.New(fiber.Config{
		AppName:      name,
		ServerHeader: name,
		ErrorHandler: errorHandler,
	})

	return a, nil
}

// Run starts the HTTP server and blocks until shutdown signal
func (a *App) Run(host string, port int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		addr := fmt.Sprintf("%s:%d", host, port)
		slog.Info("starting server", "addr", addr)
		if err := a.Fiber.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
			slog.Error("server error", "error", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down server...")

	if err := a.Fiber.Shutdown(); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	if a.DB != nil {
		if err := a.DB.Close(); err != nil {
			slog.Error("failed to close database", "error", err)
		}
	}
	if a.Cache != nil {
		if err := a.Cache.Close(); err != nil {
			slog.Error("failed to close cache", "error", err)
		}
	}

	slog.Info("server stopped")
	return nil
}

// errorHandler handles Fiber errors
func errorHandler(c fiber.Ctx, err error) error {
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
