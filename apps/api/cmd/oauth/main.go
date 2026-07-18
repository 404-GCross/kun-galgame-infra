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
	"api/pkg/oidckeys"
	"api/pkg/oidctoken"
	"api/pkg/response"

	authHandler "api/internal/platform/auth/handler"
	authRepo "api/internal/platform/auth/repository"
	authService "api/internal/platform/auth/service"
	"api/pkg/imageclient"

	artifactHandler "api/internal/platform/artifact/handler"
	artifactRepo "api/internal/platform/artifact/repository"
	artifactStorage "api/internal/platform/artifact/storage"

	"api/internal/platform/devapi"
	devapiPerm "api/internal/platform/devapi/perm"

	imgHandler "api/internal/platform/image/handler"
	imgRepoPkg "api/internal/platform/image/repository"
	imgService "api/internal/platform/image/service"
	imgStorage "api/internal/platform/image/storage"

	siteHandler "api/internal/platform/site/handler"
	sitePerm "api/internal/platform/site/perm"
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
	siteRoleRepo := authRepo.NewUserSiteRoleRepository(db)
	oauthClientRepo := siteRepo.NewOAuthClientRepository(db)
	siteRepository := siteRepo.NewSiteRepository(db)
	signingKeyRepo := authRepo.NewSigningKeyRepository(db)

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
	oauthSvc := authService.NewOAuthService(userRepo, authCodeRepo, sessionRepo, oauthClientRepo, siteRoleRepo, cfg)
	adminSvc := authService.NewAdminService(userRepo, sessionRepo, siteRoleRepo, siteRepository, imgCli)
	userBatchSvc := authService.NewUserBatchService(userRepo, siteRoleRepo)
	creatorAppSvc := authService.NewCreatorApplicationService(authRepo.NewCreatorApplicationRepository(db), userRepo, userBatchSvc)
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
	creatorAppH := authHandler.NewCreatorApplicationHandler(creatorAppSvc)

	var avatarUploadH *authHandler.AvatarUploadHandler
	if imgCli != nil {
		avatarUploadH = authHandler.NewAvatarUploadHandler(a.DB.DB(), imgCli)
	}
	siteH := siteHandler.NewSiteHandler(siteSvc)

	// OIDC signing keys + public metadata (Phase 0). Gated on
	// KUN_OIDC_KEY_ENC_KEY: when unset, key bootstrap + the jwks/discovery
	// endpoints are skipped (dev without OIDC). Bootstrap is idempotent —
	// generates one active ES256 + one active RS256 key on first run.
	// See docs/auth/03-oidc-standardization-design.md §4/§5.4.
	var oidcH *authHandler.OIDCHandler
	if cfg.OIDC.KeyEncKey != "" {
		signingKeySvc := authService.NewSigningKeyService(signingKeyRepo, cfg.OIDC.KeyEncKey)
		if err := signingKeySvc.EnsureBootstrapped(cleanupCtx); err != nil {
			slog.Error("oidc signing key bootstrap failed", "err", err)
		}
		oidcH = authHandler.NewOIDCHandler(signingKeySvc, cfg)

		// Phase 1 token signing/verification. The verifier is accept-both
		// (ES256/RS256 via the local key set + HS256 legacy) and is set
		// regardless of the sign flag, so this OP verifies ES256 tokens before
		// any signer flips. The access-token signer uses ES256 only when
		// KUN_OIDC_SIGN_ASYMMETRIC is on (and an active key exists); else HS256.
		// See docs/auth/03-oidc-standardization-design.md §10 Phase 1.
		verifier := oidctoken.NewVerifier(cfg.JWT.Secret, signingKeySvc)
		var signer oidctoken.Signer = oidctoken.NewHS256Signer(cfg.JWT.Secret, cfg.OIDC.Issuer)
		// id_token: always asymmetric (independent of the access-token flag) and
		// signed RS256 — the OIDC registration default id_token_signed_response_alg,
		// so standard RP libraries verify it without per-client alg configuration.
		if kid, key, err := signingKeySvc.ActiveSigner(cleanupCtx, oidckeys.AlgRS256); err != nil {
			slog.Error("no active RS256 signing key; id_token issuance disabled", "err", err)
		} else {
			oauthSvc.WithIDSigner(oidctoken.NewIDSigner(kid, oidckeys.AlgRS256, key, cfg.OIDC.Issuer))
		}
		if kid, key, err := signingKeySvc.ActiveSigner(cleanupCtx, oidckeys.AlgES256); err != nil {
			slog.Error("no active ES256 signing key; asymmetric access-token signing unavailable", "err", err)
		} else if cfg.OIDC.SignAsymmetric {
			signer = oidctoken.NewES256Signer(kid, key, cfg.OIDC.Issuer)
			slog.Info("oauth signing access tokens with ES256", "kid", kid)
		}
		authSvc.WithTokenSigner(signer).WithTokenVerifier(verifier)
		oauthSvc.WithTokenSigner(signer)
	} else {
		slog.Warn("KUN_OIDC_KEY_ENC_KEY unset; OIDC signing keys + jwks/discovery disabled")
	}

	// Global middleware
	a.Fiber.Use(middleware.RequestID())
	a.Fiber.Use(middleware.Logger())

	// Liveness probe — root /healthz, registered before CORS/rate-limit so the
	// container HEALTHCHECK (the `healthcheck` subcommand) isn't throttled or
	// CORS-gated. Unified to /healthz across all services.
	a.Fiber.Get("/healthz", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// Public OIDC/OAuth metadata — world-readable (handlers set ACAO *),
	// registered BEFORE the origin-restricted CORS middleware so browser-based
	// OIDC clients can fetch discovery/JWKS cross-origin.
	// See docs/auth/03-oidc-standardization-design.md §5.4.
	if oidcH != nil {
		a.Fiber.Get("/oauth/jwks", oidcH.JWKS)
		a.Fiber.Get("/.well-known/openid-configuration", oidcH.Discovery)
		a.Fiber.Get("/.well-known/oauth-authorization-server", oidcH.Discovery)
	}

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
	// Multi-account session bag (account chooser, same-site). See
	// docs/integration/oauth/09-account-switching.md.
	authProtected.Get("/sessions", authH.ListSessions)
	authProtected.Post("/sessions/switch", authH.SwitchSession)
	authProtected.Post("/sessions/logout", authH.LogoutAccount)
	authProtected.Post("/sessions/logout-all", authH.LogoutAll)
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
	// Public: build a VALIDATED error redirect for the consent page's deny path
	// + prompt=none silent errors (redirect_uri is checked against the client
	// here so a crafted one can't open-redirect). See oauth_handler.AuthorizeError.
	oauth.Post("/authorize/error", oauthH.AuthorizeError)
	// Public app directory: GET /oauth/ecosystem → the opt-in (listed) clients
	// for the "one account → these sites" strip. See
	// docs/integration/oauth/10-app-directory.md.
	oauth.Get("/ecosystem", oauthH.GetEcosystem)
	// Public: validates a post-logout redirect against a client's registered
	// redirect_uris (origin match) for the OP logout page. See
	// docs/integration/oauth/07-logout.md.
	oauth.Get("/post-logout-redirect", oauthH.PostLogoutRedirect)
	// RP-initiated logout entrypoint (browser top-level nav). Bounces to the
	// OP frontend /auth/logout page. Symmetric with /oauth/authorize. OIDC
	// RP-Initiated Logout requires the end_session_endpoint to accept GET+POST.
	oauth.Get("/logout", oauthH.LogoutRedirect)
	oauth.Post("/logout", oauthH.LogoutRedirect)
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

	// Creator-role application (apply -> admin review -> approve/decline).
	// Eligibility is gated downstream (forum/moyu); these are the central queue.
	creator := v1.Group("/creator", middleware.Auth(authSvc))
	creator.Post("/applications", creatorAppH.Apply)
	creator.Get("/applications/me", creatorAppH.MyApplication)

	// Admin routes (oauth.admin_access = admin/ren)
	admin := v1.Group("/admin", middleware.Auth(authSvc), middleware.RequirePermission(sitePerm.Resolver, sitePerm.AdminAccess))
	admin.Get("/users", adminH.ListUsers)
	admin.Get("/users/:uuid", adminH.GetUser)
	admin.Patch("/users/:uuid", adminH.UpdateUser)
	admin.Post("/users/:uuid/ban", adminH.BanUser)
	admin.Post("/users/:uuid/unban", adminH.UnbanUser)
	admin.Post("/users/:uuid/anonymize", adminH.AnonymizeUser)
	admin.Delete("/users/:uuid/sessions", adminH.DeleteUserSessions)
	// Role management. Group requires oauth.admin_access so admin + ren both
	// pass; the per-role matrix (admin→moderator/creator only, ren→all except
	// ren) is enforced in the handler (callerCanManageRole). `ren` is never
	// grantable via API.
	admin.Post("/users/:uuid/roles", adminH.AssignRole)
	admin.Delete("/users/:uuid/roles/:role", adminH.RevokeRole)
	// Site-scoped role grants (docs/integration/oauth/12-site-roles.md). Gated
	// by oauth.roles.grant_site (admin/ren) — site roles sit below the global
	// moderator, so an admin may grant them.
	admin.Post("/users/:uuid/site-roles",
		middleware.RequirePermission(sitePerm.Resolver, sitePerm.RolesGrantSite), adminH.AssignSiteRole)
	admin.Delete("/users/:uuid/site-roles",
		middleware.RequirePermission(sitePerm.Resolver, sitePerm.RolesGrantSite), adminH.RevokeSiteRole)
	// Creator application review queue.
	admin.Get("/creator/applications", creatorAppH.AdminList)
	admin.Post("/creator/applications/:id/approve", creatorAppH.AdminApprove)
	admin.Post("/creator/applications/:id/decline", creatorAppH.AdminDecline)
	admin.Post("/users/:uuid/moemoepoint", moemoepointH.AdminAdjust)
	admin.Get("/users/:uuid/moemoepoint/log", moemoepointH.AdminGetLog)
	// Registration analytics — under /stats so it can't collide with the
	// /users/:uuid param route.
	admin.Get("/stats/registrations", adminH.RegistrationStats)
	admin.Get("/stats/registrations/hourly", adminH.HourlyRegistrationStats)
	if avatarUploadH != nil {
		admin.Post("/users/:uuid/avatar", avatarUploadH.Upload)
	}

	// Site routes (oauth.admin_access = admin/ren)
	sites := v1.Group("/sites", middleware.Auth(authSvc), middleware.RequirePermission(sitePerm.Resolver, sitePerm.AdminAccess))
	sites.Get("/", siteH.List)
	sites.Post("/", siteH.Create)
	sites.Get("/:id", siteH.Get)
	sites.Put("/:id", siteH.Update)
	sites.Delete("/:id", siteH.Delete)
	sites.Get("/:id/clients", siteH.GetSiteClients)

	// OAuth client routes (oauth.admin_access = admin/ren)
	oauthClients := v1.Group("/oauth/clients", middleware.Auth(authSvc), middleware.RequirePermission(sitePerm.Resolver, sitePerm.AdminAccess))
	oauthClients.Get("/", siteH.ListClients)
	oauthClients.Post("/", siteH.CreateClient)
	// Admin app-directory logo upload → image_service, returns {hash,url}; the
	// form saves url into the client's logo_url. Decoupled from :id so it works
	// at create time too. See docs/integration/oauth/10-app-directory.md.
	if avatarUploadH != nil {
		oauthClients.Post("/logo", avatarUploadH.UploadClientLogo)
	}
	oauthClients.Put("/:id", siteH.UpdateClient)
	oauthClients.Put("/:id/storage", middleware.RequirePermission(sitePerm.Resolver, sitePerm.ClientsStorageConfig), siteH.UpdateClientStorage)
	oauthClients.Delete("/:id", siteH.DeleteClient)

	// Developer-platform (NextMoe open API). One repository + one AdminService
	// back BOTH the admin management face and the developer self-service face, so
	// the API-key lifecycle (mint/rotate/revoke + resolve-cache busting) has a
	// single implementation.
	//   - Admin face (/admin/devapi/*): admin/ren, devapi.manage — enable apps,
	//     set tier/quota, mint/rotate/revoke any app's keys.
	//   - Self-service face (/dev/*): user JWT only, owner-guarded — a developer
	//     builds + manages their OWN apps/keys.
	// The public read faces are NOT here — they ship with 02/03.
	// See docs/developer-platform/01-design.md §5-9.
	devRepo := devapi.NewRepository(db)
	devAdminSvc := devapi.NewAdminService(devRepo, devapi.NewRedisStore(a.Cache))
	devAdminH := devapi.NewAdminHandler(devAdminSvc)
	devGroup := admin.Group("/devapi", middleware.RequirePermission(devapiPerm.Resolver, devapiPerm.Manage))
	devAdminH.Register(devGroup)

	// Developer self-service face. User-JWT auth (no admin permission); every
	// endpoint is owner-guarded in the service (non-owner → 404, no existence
	// leak). App/key caps are constants (5 apps/user, 5 active keys/app).
	devSelfH := devapi.NewSelfServiceHandler(devapi.NewSelfServiceService(devRepo, devAdminSvc))
	devSelfGroup := v1.Group("/dev", middleware.Auth(authSvc))
	devSelfH.Register(devSelfGroup)

	// Image admin routes — best-effort; if images DB or S3 are unreachable
	// in dev, skip registration rather than failing the whole oauth service.
	registerImageAdmin(a, cfg, admin)
	registerArtifactAdmin(a, cfg, authSvc)

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

// registerArtifactAdmin wires admin endpoints for the artifact service. Like the
// image admin, these run inside the oauth service (admin auth lives here) and
// read the dedicated kun_artifacts DB. Failures are logged + skipped, not fatal.
// Delete is soft-only. Reclaim (immediate hard-clean of an interrupted upload)
// needs object-storage access, so it gets the SAME least-privilege cleanup S3
// client the artifact GC uses (built from ArtifactCleanupS3). When those creds
// aren't configured, the client is nil and Reclaim returns 503 — exactly the
// same condition under which the GC no-ops.
func registerArtifactAdmin(a *app.App, cfg *config.Config, authSvc *authService.AuthService) {
	artifactsDB, err := database.NewPostgresDB(cfg.ArtifactsDatabase)
	if err != nil {
		slog.Warn("artifact admin: artifacts db unreachable; admin endpoints disabled", "err", err)
		return
	}
	statsRepo := artifactRepo.NewStatsRepository(artifactsDB.DB())

	// Cleanup-scoped S3 client (Abort/Delete), only when creds are configured —
	// mirrors the GC's guard (RunArtifactGC skips when ArtifactS3 key is empty).
	var store *artifactStorage.Client
	if cfg.ArtifactS3.AccessKeyID != "" {
		if c, serr := artifactStorage.NewClient(cfg.ArtifactCleanupS3()); serr != nil {
			slog.Warn("artifact admin: cleanup storage init failed; reclaim disabled", "err", serr)
		} else {
			store = c
		}
	}
	adminH := artifactHandler.NewAdmin(artifactsDB.DB(), statsRepo, store, cfg.ArtifactService.ReclaimMinIdle)

	// These endpoints are served by Huma (code-first OpenAPI — see docs/artifact/10
	// §4.3). Huma registers on the app, so the /admin group's Auth+admin-access
	// gate does NOT cover them: gate the exact prefix here with path-scoped Fiber
	// middleware, registered BEFORE SetupAdmin's routes so it precedes them in the
	// stack. stats is admin-visible; list/delete/reclaim additionally require the
	// artifact.files.manage (ren) permission, enforced in-handler (admin_huma.go).
	a.Fiber.Use("/api/v1/admin/artifact", middleware.Auth(authSvc), middleware.RequirePermission(sitePerm.Resolver, sitePerm.AdminAccess))
	artifactHandler.SetupAdmin(a.Fiber, adminH)

	slog.Info("artifact admin endpoints (Huma) registered under /api/v1/admin/artifact/*")
}
