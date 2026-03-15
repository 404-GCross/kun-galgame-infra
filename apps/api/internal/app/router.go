package app

import (
	"api/internal/infrastructure/mail"
	"api/internal/middleware"

	artifactHandler "api/internal/platform/artifact/handler"
	artifactRepo "api/internal/platform/artifact/repository"
	artifactService "api/internal/platform/artifact/service"

	authHandler "api/internal/platform/auth/handler"
	authRepo "api/internal/platform/auth/repository"
	authService "api/internal/platform/auth/service"

	commentHandler "api/internal/platform/comment/handler"
	commentRepo "api/internal/platform/comment/repository"
	commentService "api/internal/platform/comment/service"

	contentHandler "api/internal/platform/content/handler"
	contentRepo "api/internal/platform/content/repository"
	contentService "api/internal/platform/content/service"

	gameHandler "api/internal/platform/game/handler"
	gameRepo "api/internal/platform/game/repository"
	gameService "api/internal/platform/game/service"

	moderationHandler "api/internal/platform/moderation/handler"
	moderationRepo "api/internal/platform/moderation/repository"
	moderationService "api/internal/platform/moderation/service"

	siteHandler "api/internal/platform/site/handler"
	siteRepo "api/internal/platform/site/repository"
	siteService "api/internal/platform/site/service"

	"github.com/gofiber/fiber/v3"
)

// setupRoutes configures all application routes
func (a *App) setupRoutes() {
	// Get database connection
	db := a.db.DB()

	// Initialize repositories
	userRepo := authRepo.NewUserRepository(db)
	sessionRepo := authRepo.NewSessionRepository(db)
	passwordResetRepo := authRepo.NewPasswordResetRepository(db)
	siteRepository := siteRepo.NewSiteRepository(db)
	gameRepository := gameRepo.NewGameRepository(db)
	contentRepository := contentRepo.NewContentRepository(db)
	commentRepository := commentRepo.NewCommentRepository(db)
	artifactRepository := artifactRepo.NewArtifactRepository(db)
	moderationRepository := moderationRepo.NewModerationRepository(db)

	// Initialize mail service
	mailer := mail.NewMailer(a.config.Mail)

	// Initialize additional repositories for OAuth
	authCodeRepo := authRepo.NewAuthorizationCodeRepository(db)
	oauthClientRepo := siteRepo.NewOAuthClientRepository(db)

	// Initialize services
	authSvc := authService.NewAuthServiceFull(userRepo, sessionRepo, passwordResetRepo, mailer, a.config)
	oauthSvc := authService.NewOAuthService(userRepo, authCodeRepo, sessionRepo, oauthClientRepo, a.config)
	adminSvc := authService.NewAdminService(userRepo, sessionRepo)
	siteSvc := siteService.NewSiteService(siteRepository)
	gameSvc := gameService.NewGameService(gameRepository)
	contentSvc := contentService.NewContentService(contentRepository)
	commentSvc := commentService.NewCommentService(commentRepository)
	artifactSvc := artifactService.NewArtifactService(artifactRepository)
	moderationSvc := moderationService.NewModerationService(moderationRepository, nil)

	// Initialize handlers
	authH := authHandler.NewAuthHandler(authSvc)
	oauthH := authHandler.NewOAuthHandler(oauthSvc)
	adminH := authHandler.NewAdminHandler(adminSvc)
	siteH := siteHandler.NewSiteHandler(siteSvc)
	gameH := gameHandler.NewGameHandler(gameSvc)
	contentH := contentHandler.NewContentHandler(contentSvc)
	commentH := commentHandler.NewCommentHandler(commentSvc)
	artifactH := artifactHandler.NewArtifactHandler(artifactSvc)
	moderationH := moderationHandler.NewModerationHandler(moderationSvc)

	// Global middleware
	a.fiber.Use(middleware.RequestID())
	a.fiber.Use(middleware.Logger())
	a.fiber.Use(middleware.CORS(a.config.Server.CORSOrigin))
	a.fiber.Use(middleware.RateLimit(a.cache))

	// API routes
	api := a.fiber.Group("/api")
	v1 := api.Group("/v1")

	// Health check
	v1.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Auth routes (public)
	auth := v1.Group("/auth")
	auth.Post("/register", authH.Register)
	auth.Post("/login", authH.Login)
	auth.Post("/refresh", authH.Refresh)
	auth.Post("/password/forgot", authH.ForgotPassword)
	auth.Post("/password/reset", authH.ResetPassword)

	// Auth routes (protected)
	authProtected := auth.Group("", middleware.Auth(authSvc))
	authProtected.Post("/logout", authH.Logout)
	authProtected.Get("/me", authH.Me)
	authProtected.Put("/password", authH.ChangePassword)

	// OAuth 2.0 routes
	oauth := v1.Group("/oauth")
	oauth.Post("/token", oauthH.Token)     // Public: exchange code or refresh token
	oauth.Post("/revoke", oauthH.Revoke)   // Public: revoke a token
	oauthProtected := oauth.Group("", middleware.Auth(authSvc))
	oauthProtected.Get("/authorize", oauthH.Authorize) // Requires login
	oauthProtected.Get("/userinfo", oauthH.UserInfo)   // Requires login

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

	// OAuth client routes
	oauthClients := v1.Group("/oauth/clients", middleware.Auth(authSvc))
	oauthClients.Get("/", siteH.ListClients)
	oauthClients.Post("/", siteH.CreateClient)

	// Game routes
	games := v1.Group("/games")
	games.Get("/", gameH.List)
	games.Get("/:id", gameH.Get)
	games.Get("/:id/revisions", gameH.ListRevisions)

	gamesProtected := games.Group("", middleware.Auth(authSvc))
	gamesProtected.Post("/", gameH.Create)
	gamesProtected.Put("/:id", gameH.Update)

	// Content routes
	contents := v1.Group("/contents")
	contents.Get("/", contentH.List)
	contents.Get("/:id", contentH.Get)

	contentsProtected := contents.Group("", middleware.Auth(authSvc))
	contentsProtected.Post("/", contentH.Create)
	contentsProtected.Put("/:id", contentH.Update)
	contentsProtected.Delete("/:id", contentH.Delete)

	// Comment routes
	comments := v1.Group("/comments")
	comments.Get("/", commentH.List)

	commentsProtected := comments.Group("", middleware.Auth(authSvc))
	commentsProtected.Post("/", commentH.Create)
	commentsProtected.Put("/:id", commentH.Update)
	commentsProtected.Delete("/:id", commentH.Delete)

	// Artifact routes
	artifacts := v1.Group("/artifacts", middleware.Auth(authSvc))
	artifacts.Get("/", artifactH.List)
	artifacts.Get("/:id", artifactH.Get)
	artifacts.Post("/", artifactH.Create)
	artifacts.Delete("/:id", artifactH.Delete)
	artifacts.Get("/:id/download", artifactH.Download)

	// Moderation routes (admin/moderator only)
	moderation := v1.Group("/moderation", middleware.Auth(authSvc), middleware.RequireRole("admin", "moderator"))
	moderation.Get("/jobs", moderationH.ListJobs)
	moderation.Get("/jobs/:id", moderationH.GetJob)
	moderation.Post("/jobs/:id/review", moderationH.ManualReview)
	moderation.Get("/policies", moderationH.ListPolicies)
}
