package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/mail"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/logger"

	authHandler "api/internal/platform/auth/handler"
	authRepo "api/internal/platform/auth/repository"
	authService "api/internal/platform/auth/service"

	siteHandler "api/internal/platform/site/handler"
	siteRepo "api/internal/platform/site/repository"
	siteService "api/internal/platform/site/service"

	"github.com/gofiber/fiber/v3"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Init(cfg.Server.Env)

	application, err := app.New(cfg, app.Options{
		Name:      "kun-oauth",
		NeedCache: true,
	})
	if err != nil {
		slog.Error("failed to create application", "error", err)
		os.Exit(1)
	}

	setupRoutes(application, cfg)

	if err := application.Run(cfg.Server.Host, cfg.Server.Port); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}

func setupRoutes(a *app.App, cfg *config.Config) {
	db := a.DB.DB()

	// Repositories
	userRepo := authRepo.NewUserRepository(db)
	sessionRepo := authRepo.NewSessionRepository(db)
	passwordResetRepo := authRepo.NewPasswordResetRepository(db)
	authCodeRepo := authRepo.NewAuthorizationCodeRepository(db)
	oauthClientRepo := siteRepo.NewOAuthClientRepository(db)
	siteRepository := siteRepo.NewSiteRepository(db)

	// Services
	mailer := mail.NewMailer(cfg.Mail)
	authSvc := authService.NewAuthServiceFull(userRepo, sessionRepo, passwordResetRepo, mailer, a.Cache, cfg)
	oauthSvc := authService.NewOAuthService(userRepo, authCodeRepo, sessionRepo, oauthClientRepo, cfg)
	adminSvc := authService.NewAdminService(userRepo, sessionRepo)
	siteSvc := siteService.NewSiteService(siteRepository, oauthClientRepo)

	// Handlers
	authH := authHandler.NewAuthHandler(authSvc, cfg)
	oauthH := authHandler.NewOAuthHandler(oauthSvc, cfg)
	adminH := authHandler.NewAdminHandler(adminSvc)
	siteH := siteHandler.NewSiteHandler(siteSvc)

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())
	a.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))
	a.Fiber.Use(middleware.RateLimit(a.Cache))

	// API routes
	api := a.Fiber.Group("/api")
	v1 := api.Group("/v1")

	// Health check
	v1.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Strict rate limiter for sensitive endpoints
	strict := middleware.StrictRateLimit(a.Cache)

	// Auth routes (public)
	auth := v1.Group("/auth")
	auth.Post("/register", strict, authH.Register)
	auth.Post("/login", strict, authH.Login)
	auth.Post("/refresh", authH.Refresh)
	auth.Post("/password/forgot", strict, authH.ForgotPassword)
	auth.Post("/password/reset", strict, authH.ResetPassword)

	// Auth routes (protected)
	authProtected := auth.Group("", middleware.Auth(authSvc))
	authProtected.Post("/logout", authH.Logout)
	authProtected.Get("/me", authH.Me)
	authProtected.Put("/password", authH.ChangePassword)
	authProtected.Post("/email/send-code", authH.SendEmailChangeCode)
	authProtected.Put("/email", authH.ChangeEmail)

	// OAuth 2.0 routes
	oauth := v1.Group("/oauth")
	oauth.Post("/token", strict, oauthH.Token)
	oauth.Post("/revoke", oauthH.Revoke)
	oauth.Get("/authorize", middleware.OptionalAuth(authSvc), oauthH.Authorize)
	oauthProtected := oauth.Group("", middleware.Auth(authSvc))
	oauthProtected.Post("/authorize/consent", oauthH.Consent)
	oauthProtected.Get("/userinfo", oauthH.UserInfo)

	// User routes
	users := v1.Group("/users", middleware.Auth(authSvc))
	users.Get("/:uuid", authH.GetProfile)

	// Admin routes (admin only)
	admin := v1.Group("/admin", middleware.Auth(authSvc), middleware.RequireRole("admin"))
	admin.Get("/users", adminH.ListUsers)
	admin.Get("/users/:uuid", adminH.GetUser)
	admin.Patch("/users/:uuid", adminH.UpdateUser)
	admin.Post("/users/:uuid/ban", adminH.BanUser)
	admin.Post("/users/:uuid/unban", adminH.UnbanUser)
	admin.Delete("/users/:uuid/sessions", adminH.DeleteUserSessions)

	// Site routes (admin only)
	sites := v1.Group("/sites", middleware.Auth(authSvc), middleware.RequireRole("admin"))
	sites.Get("/", siteH.List)
	sites.Post("/", siteH.Create)
	sites.Get("/:id", siteH.Get)
	sites.Put("/:id", siteH.Update)
	sites.Delete("/:id", siteH.Delete)
	sites.Get("/:id/clients", siteH.GetSiteClients)

	// OAuth client routes (admin only)
	oauthClients := v1.Group("/oauth/clients", middleware.Auth(authSvc), middleware.RequireRole("admin"))
	oauthClients.Get("/", siteH.ListClients)
	oauthClients.Post("/", siteH.CreateClient)
	oauthClients.Put("/:id", siteH.UpdateClient)
	oauthClients.Delete("/:id", siteH.DeleteClient)
}
