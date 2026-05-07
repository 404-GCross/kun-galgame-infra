package main

import (
	"context"
	"log/slog"
	"os"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/infrastructure/mail"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/logger"

	authHandler "api/internal/platform/auth/handler"
	authRepo "api/internal/platform/auth/repository"
	authService "api/internal/platform/auth/service"
	"api/pkg/imageclient"

	imgHandler "api/internal/platform/image/handler"
	imgRepoPkg "api/internal/platform/image/repository"
	imgService "api/internal/platform/image/service"
	imgStorage "api/internal/platform/image/storage"

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

	cleanupCtx, cancelCleanup := context.WithCancel(context.Background())
	defer cancelCleanup()

	setupRoutes(application, cfg, cleanupCtx)

	if err := application.Run(cfg.Server.Host, cfg.Server.Port); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}

func setupRoutes(a *app.App, cfg *config.Config, cleanupCtx context.Context) {
	db := a.DB.DB()

	// Repositories
	userRepo := authRepo.NewUserRepository(db)
	sessionRepo := authRepo.NewSessionRepository(db)
	passwordResetRepo := authRepo.NewPasswordResetRepository(db)
	authCodeRepo := authRepo.NewAuthorizationCodeRepository(db)
	oauthClientRepo := siteRepo.NewOAuthClientRepository(db)
	siteRepository := siteRepo.NewSiteRepository(db)

	// Start background cleanup for expired sessions and authorization codes
	app.StartCleanup(cleanupCtx, sessionRepo, authCodeRepo)

	// Services
	mailer := mail.NewMailer(cfg.Mail)
	authSvc := authService.NewAuthServiceFull(userRepo, sessionRepo, passwordResetRepo, mailer, a.Cache, cfg)
	oauthSvc := authService.NewOAuthService(userRepo, authCodeRepo, sessionRepo, oauthClientRepo, cfg)
	adminSvc := authService.NewAdminService(userRepo, sessionRepo)
	userBatchSvc := authService.NewUserBatchService(userRepo)
	siteSvc := siteService.NewSiteService(siteRepository, oauthClientRepo)

	// Handlers
	authH := authHandler.NewAuthHandler(authSvc, cfg)
	oauthH := authHandler.NewOAuthHandler(oauthSvc, cfg)
	adminH := authHandler.NewAdminHandler(adminSvc)
	userBatchH := authHandler.NewUserBatchHandler(userBatchSvc)

	// Avatar upload handler (calls image_service via SDK). Singleton.
	// Nil if KUN_IMAGE_CLIENT_ID/SECRET unset → endpoint refused with clear error.
	var avatarUploadH *authHandler.AvatarUploadHandler
	if cfg.ImageClient.ClientID != "" && cfg.ImageClient.ClientSecret != "" {
		imgCli := imageclient.New(imageclient.Config{
			BaseURL:      cfg.ImageClient.BaseURL,
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     cfg.ImageClient.ClientID,
			ClientSecret: cfg.ImageClient.ClientSecret,
		})
		avatarUploadH = authHandler.NewAvatarUploadHandler(a.DB.DB(), imgCli)
		slog.Info("image client configured for avatar uploads")
	} else {
		slog.Warn("image client not configured; /admin/users/:uuid/avatar disabled")
	}
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
	oauth.Get("/authorize", oauthH.Authorize)
	oauthProtected := oauth.Group("", middleware.Auth(authSvc))
	oauthProtected.Post("/authorize/consent", oauthH.Consent)
	oauthProtected.Get("/userinfo", oauthH.UserInfo)

	// User routes
	// Cross-service endpoints (kungal / moyu / galgame_wiki backends).
	// Auth is OAuth Client Basic Auth — service-to-service, not end-user JWT.
	// Registered BEFORE the dynamic `/users/:uuid` group so Fiber matches
	// these literal paths before falling through to the param route.
	v1.Get("/users/batch",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		userBatchH.Get,
	)
	v1.Get("/users/search",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		userBatchH.Search,
	)

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
	if avatarUploadH != nil {
		admin.Post("/users/:uuid/avatar", avatarUploadH.Upload)
	}

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

	// Image admin routes — best-effort; if images DB or S3 are unreachable
	// in dev, skip registration rather than failing the whole oauth service.
	registerImageAdmin(a, cfg, admin)
}

// registerImageAdmin wires admin endpoints for the image service. These
// endpoints run inside the oauth service (not cmd/image) because admin
// auth lives here. Failures are logged and skipped, not fatal.
func registerImageAdmin(_ *app.App, cfg *config.Config, admin fiber.Router) {
	imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Warn("image admin: images db unreachable; admin endpoints disabled", "err", err)
		return
	}
	s3, err := imgStorage.NewClient(cfg.ImageS3)
	if err != nil {
		slog.Warn("image admin: s3 unreachable; admin endpoints disabled", "err", err)
		return
	}

	imgRepo := imgRepoPkg.NewImageRepository(imagesDB.DB())
	usageRepo := imgRepoPkg.NewSiteUsageRepository(imagesDB.DB())
	statsRepo := imgRepoPkg.NewStatsRepository(imagesDB.DB())
	// Service here is used only for MainURL/VariantURL formatting; no
	// upload flow runs on the admin service.
	svc := imgService.New(nil, s3, imgRepo, usageRepo, cfg.ImageService.CDNBase)
	adminH := imgHandler.NewAdmin(imagesDB.DB(), svc, statsRepo, s3)

	g := admin.Group("/image")
	g.Get("/list", adminH.List)
	g.Get("/stats", adminH.Stats)
	g.Patch("/:hash/review", adminH.Review)
	g.Delete("/:hash", adminH.Delete)

	slog.Info("image admin endpoints registered under /api/v1/admin/image/*")
}
