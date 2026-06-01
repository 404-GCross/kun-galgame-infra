package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"api/internal/app"
	"api/internal/infrastructure/database"
	"api/internal/infrastructure/mail"
	"api/internal/jobs"
	"api/internal/middleware"
	"api/pkg/config"
	"api/pkg/health"
	"api/pkg/logger"
	"api/pkg/response"

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

	// `healthcheck` subcommand for container HEALTHCHECK (distroless has no
	// shell/curl). No-op for a normal start; exits before any infra is touched.
	health.MaybeProbe(cfg.Server.Port, "/healthz")

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

	// Image SDK client (server-to-server). Created up-front so both the
	// avatar upload handler AND AdminService (avatar GC on anonymize) can
	// share it. Nil when KUN_IMAGE_CLIENT_ID/SECRET unset → avatar upload
	// disabled + avatar GC-on-anonymize skipped (anonymize still nulls the
	// reference, so the binary is reclaimed by image_service's own GC).
	var imgCli *imageclient.Client
	if cfg.ImageClient.ClientID != "" && cfg.ImageClient.ClientSecret != "" {
		imgCli = imageclient.New(imageclient.Config{
			BaseURL:      cfg.ImageClient.BaseURL,
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     cfg.ImageClient.ClientID,
			ClientSecret: cfg.ImageClient.ClientSecret,
		})
		slog.Info("image client configured for avatar uploads + GC")
	} else {
		slog.Warn("image client not configured; avatar upload + avatar GC disabled")
	}

	authSvc := authService.NewAuthServiceFull(userRepo, sessionRepo, passwordResetRepo, mailer, a.Cache, cfg)
	oauthSvc := authService.NewOAuthService(userRepo, authCodeRepo, sessionRepo, oauthClientRepo, cfg)
	adminSvc := authService.NewAdminService(userRepo, sessionRepo, imgCli)
	userBatchSvc := authService.NewUserBatchService(userRepo)
	moemoepointSvc := authService.NewMoemoepointService(a.DB.DB(), userRepo)
	// Registration grants a welcome gift via the moemoepoint ledger.
	authSvc.WithMoemoepoint(moemoepointSvc)
	siteSvc := siteService.NewSiteService(siteRepository, oauthClientRepo)

	// Handlers
	authH := authHandler.NewAuthHandler(authSvc, cfg)
	oauthH := authHandler.NewOAuthHandler(oauthSvc, cfg)
	adminH := authHandler.NewAdminHandler(adminSvc)
	moemoepointH := authHandler.NewMoemoepointHandler(moemoepointSvc)
	userBatchH := authHandler.NewUserBatchHandler(userBatchSvc)

	var avatarUploadH *authHandler.AvatarUploadHandler
	if imgCli != nil {
		avatarUploadH = authHandler.NewAvatarUploadHandler(a.DB.DB(), imgCli)
	}
	siteH := siteHandler.NewSiteHandler(siteSvc)

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())

	// Liveness probe — root /healthz, registered before CORS/rate-limit so the
	// container HEALTHCHECK (the `healthcheck` subcommand) isn't throttled or
	// CORS-gated. Unified to /healthz across all services.
	a.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	a.Fiber.Use(middleware.CORS(cfg.Server.CORSOrigin))
	a.Fiber.Use(middleware.RateLimit(a.Cache))

	// API routes
	api := a.Fiber.Group("/api")
	v1 := api.Group("/v1")

	// Strict rate limiter for genuinely anonymous brute-force targets
	// (login / register / password reset). NOT used on /oauth/token —
	// see oauthTokenLimiter below.
	strict := middleware.StrictRateLimit(a.Cache)

	// Per-client limiter for /oauth/token. Keyed by client_id so a
	// confidential SSR backend (kungal/moyu) proxying its whole userbase
	// through one IP isn't throttled to the strict 10/min/IP bucket.
	oauthTokenLimiter := middleware.OAuthTokenRateLimit(a.Cache)

	// Auth routes (public)
	auth := v1.Group("/auth")
	// Two-step registration: send code, then submit registration with code.
	// Both behind `strict` (10/min/IP) — abuse vectors are email spam +
	// account enumeration via name/email-exists errors.
	auth.Post("/register/send-code", strict, authH.SendRegisterCode)
	auth.Post("/register", strict, authH.Register)
	auth.Post("/login", strict, authH.Login)
	auth.Post("/refresh", authH.Refresh)
	auth.Post("/password/forgot", strict, authH.ForgotPassword)
	auth.Post("/password/reset", strict, authH.ResetPassword)

	// Auth routes (protected)
	authProtected := auth.Group("", middleware.Auth(authSvc))
	authProtected.Post("/logout", authH.Logout)
	authProtected.Get("/me", authH.Me)
	authProtected.Patch("/me", authH.UpdateProfile)
	// Self-service moemoepoint ledger: the user's OWN audit rows (reduced
	// view — no admin note/actor). Balance itself is already on /auth/me.
	authProtected.Get("/me/moemoepoint/log", moemoepointH.MyLog)
	authProtected.Put("/password", authH.ChangePassword)
	authProtected.Post("/email/send-code", authH.SendEmailChangeCode)
	authProtected.Put("/email", authH.ChangeEmail)
	// End-user avatar upload: multipart `file=`, writes users.avatar_image_hash
	// and returns the image_service result. Registered only when an image
	// client is configured (same gate as the admin variant below).
	if avatarUploadH != nil {
		authProtected.Post("/me/avatar", avatarUploadH.UploadMine)
	}

	// OAuth 2.0 routes
	oauth := v1.Group("/oauth")
	oauth.Post("/token", oauthTokenLimiter, oauthH.Token)
	oauth.Post("/revoke", oauthH.Revoke)
	oauth.Get("/authorize", oauthH.Authorize)
	// Public metadata: GET /oauth/client-info?client_id=X
	// Powers the OAuth web /oauth/authorize page's auto-consent decision +
	// "你将授权访问 X" display. Returns only safe fields (name, auto_consent,
	// site_domain). See ClientPublicInfo + docs/integration/oauth/05-registration.md.
	oauth.Get("/client-info", oauthH.GetClientPublic)
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
	// Moemoepoint service-to-service (kungal / moyu award/deduct + read).
	// Basic Auth; numeric user id. 3-segment paths so they don't collide
	// with the `/users/:uuid` JWT group below.
	v1.Post("/users/:id/moemoepoint",
		middleware.OAuthClientBasicAuth(oauthClientRepo), moemoepointH.Adjust)
	v1.Get("/users/:id/moemoepoint",
		middleware.OAuthClientBasicAuth(oauthClientRepo), moemoepointH.GetBalance)
	v1.Get("/users/:id/moemoepoint/log",
		middleware.OAuthClientBasicAuth(oauthClientRepo), moemoepointH.GetLog)

	users := v1.Group("/users", middleware.Auth(authSvc))
	users.Get("/:uuid", authH.GetProfile)

	// Admin routes (admin only)
	admin := v1.Group("/admin", middleware.Auth(authSvc), middleware.RequireRole("admin"))
	admin.Get("/users", adminH.ListUsers)
	admin.Get("/users/:uuid", adminH.GetUser)
	admin.Patch("/users/:uuid", adminH.UpdateUser)
	admin.Post("/users/:uuid/ban", adminH.BanUser)
	admin.Post("/users/:uuid/unban", adminH.UnbanUser)
	admin.Post("/users/:uuid/anonymize", adminH.AnonymizeUser)
	admin.Delete("/users/:uuid/sessions", adminH.DeleteUserSessions)
	admin.Post("/users/:uuid/moemoepoint", moemoepointH.AdminAdjust)
	admin.Get("/users/:uuid/moemoepoint/log", moemoepointH.AdminGetLog)
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

	// Job registry: in-process scheduler (default auto-run) + admin
	// trigger/visibility. Pure docker-compose: scheduler lives in this
	// long-lived container; cross-replica single-flight via PG advisory
	// lock in the runner. Design: docs/jobs/01-implementation-plan.md.
	jobReg := jobs.NewRegistry()
	jobs.RegisterAll(jobReg)
	jobRunner := jobs.NewRunner(cfg, db)
	jobs.StartScheduler(cleanupCtx, jobReg, jobRunner)
	registerJobsAdmin(admin, jobReg, jobRunner)
}

// registerJobsAdmin wires the job registry admin endpoints. Mirrors
// registerImageAdmin: admin auth already applied on the group.
func registerJobsAdmin(admin fiber.Router, reg *jobs.Registry, runner *jobs.Runner) {
	g := admin.Group("/jobs")

	// GET /api/v1/admin/jobs — registered jobs + each one's latest run.
	g.Get("", func(c fiber.Ctx) error {
		type jobView struct {
			Name      string `json:"name"`
			Desc      string `json:"desc"`
			DailyAt   string `json:"daily_at,omitempty"`
			Auto      bool   `json:"auto"`
			LatestRun any    `json:"latest_run"`
		}
		out := make([]jobView, 0)
		for _, j := range reg.List() {
			latest, err := runner.LatestRun(c.Context(), j.Name)
			if err != nil {
				slog.Error("jobs admin: latest run", "job", j.Name, "err", err)
			}
			out = append(out, jobView{
				Name:      j.Name,
				Desc:      j.Desc,
				DailyAt:   j.Schedule.DailyAt,
				Auto:      !j.Schedule.Zero(),
				LatestRun: latest,
			})
		}
		return response.Success(c, out)
	})

	// POST /api/v1/admin/jobs/:name/run — manual trigger (background).
	g.Post("/:name/run", func(c fiber.Ctx) error {
		name := c.Params("name")
		job, ok := reg.Get(name)
		if !ok {
			return response.Error(c, fiber.StatusNotFound, fiber.StatusNotFound, "unknown job: "+name)
		}
		runner.RunAsync(job, jobs.TriggerAdmin)
		return response.SuccessWithMessage(c, "job triggered (running in background)", fiber.Map{"job": name})
	})

	// GET /api/v1/admin/jobs/:name/runs?limit=20 — run history.
	g.Get("/:name/runs", func(c fiber.Ctx) error {
		name := c.Params("name")
		if _, ok := reg.Get(name); !ok {
			return response.Error(c, fiber.StatusNotFound, fiber.StatusNotFound, "unknown job: "+name)
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		runs, err := runner.ListRuns(c.Context(), name, limit)
		if err != nil {
			slog.Error("jobs admin: list runs", "job", name, "err", err)
			return response.Error(c, fiber.StatusInternalServerError, fiber.StatusInternalServerError, "failed to list runs")
		}
		return response.Success(c, runs)
	})

	slog.Info("jobs admin endpoints registered under /api/v1/admin/jobs/*")
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
