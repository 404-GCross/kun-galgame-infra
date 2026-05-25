package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	// Import all models
	jobsModel "api/internal/jobs/model"
	artifactModel "api/internal/platform/artifact/model"
	authModel "api/internal/platform/auth/model"
	moderationModel "api/internal/platform/moderation/model"
	siteModel "api/internal/platform/site/model"

	"gorm.io/gorm"
)

func main() {
	// Parse flags
	dropTables := flag.Bool("drop", false, "Drop all tables before migration (DANGEROUS)")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize logger
	logger.Init(cfg.Server.Env)

	// Connect to database
	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	gormDB := db.DB()

	// Drop tables if requested
	if *dropTables {
		slog.Warn("Dropping all tables...")
		if err := dropAllTables(gormDB); err != nil {
			slog.Error("failed to drop tables", "error", err)
			os.Exit(1)
		}
		slog.Info("All tables dropped")
	}

	// Run migrations
	slog.Info("Running migrations...")

	// Get all models to migrate
	models := getAllModels()

	if err := gormDB.AutoMigrate(models...); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	slog.Info("Migrations completed successfully")

	// Create initial data if needed
	if err := seedInitialData(gormDB); err != nil {
		slog.Error("failed to seed initial data", "error", err)
		os.Exit(1)
	}

	slog.Info("Database setup completed")
}

// getAllModels returns all models to be migrated
func getAllModels() []any {
	return []any{
		// Auth models
		&authModel.User{},
		&authModel.Session{},
		&authModel.OAuthAccount{},
		&authModel.UserFollow{},
		&authModel.UserSiteData{},
		&authModel.UserMigration{},
		&authModel.PasswordReset{},
		&authModel.AuthorizationCode{},

		// Site models
		&siteModel.Site{},
		&siteModel.OAuthClient{},
		&siteModel.Role{},
		&siteModel.Permission{},

		// Artifact models
		&artifactModel.Artifact{},
		&artifactModel.Manifest{},

		// Moderation models
		&moderationModel.Job{},
		&moderationModel.Result{},

		// Job registry observability
		&jobsModel.JobRun{},
	}
}

// dropAllTables drops all tables (for development only)
func dropAllTables(db *gorm.DB) error {
	models := getAllModels()
	// Reverse order to handle foreign keys
	for i := len(models) - 1; i >= 0; i-- {
		if err := db.Migrator().DropTable(models[i]); err != nil {
			// Ignore errors (table might not exist)
			slog.Debug("drop table skipped", "error", err)
		}
	}
	// Also drop the join tables
	if err := db.Migrator().DropTable("user_roles"); err != nil {
		slog.Debug("drop user_roles skipped", "error", err)
	}
	if err := db.Migrator().DropTable("role_permissions"); err != nil {
		slog.Debug("drop role_permissions skipped", "error", err)
	}
	return nil
}

// seedInitialData creates initial required data
func seedInitialData(db *gorm.DB) error {
	// Sites — mirrors the production deployment (snapshot 2026-05). Idempotent:
	// the per-domain WHERE-First check skips rows that already exist, so re-
	// running migrate after admin manually edits site metadata via UI does
	// NOT clobber their changes.
	//
	// OAuth clients are NOT seeded here — they carry per-environment
	// secrets that have no business sitting in source. Admin creates each
	// client manually through the OAuth admin UI after sites are seeded;
	// the UI's "create client" path generates a fresh secret and shows
	// it once (consumer-side `.env` then pastes it in).
	//
	// To add a new first-party site:
	//   1. Append to this slice (and update the prod DB by re-running migrate).
	//   2. Have admin create the OAuth client via UI on the new site.
	//   3. Optionally add the domain to `firstPartyDomains` below so the
	//      auto_consent backfill catches it (or admin toggles in the UI).
	sites := []siteModel.Site{
		{Name: "鲲 Galgame OAuth", Domain: "oauth.kungal.com", Description: "鲲 Galgame OAuth"},
		{Name: "鲲 Galgame 论坛", Domain: "www.kungal.com", Description: "鲲 Galgame 论坛"},
		{Name: "鲲 Galgame 补丁", Domain: "www.moyu.moe", Description: "鲲 Galgame 补丁"},
		{Name: "鲲 Galgame Wiki", Domain: "wiki.kungal.com", Description: "Galgame Wiki"},
		{Name: "鲲 Galgame AI", Domain: "ai.kungal.com", Description: "鲲 Galgame AI"},
		{Name: "鲲 Galgame 表情包", Domain: "sticker.kungal.com", Description: "鲲 Galgame 表情包"},
	}

	for _, s := range sites {
		var existing siteModel.Site
		if err := db.Where("domain = ?", s.Domain).First(&existing).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				if err := db.Create(&s).Error; err != nil {
					slog.Error("failed to create site", "domain", s.Domain, "error", err)
					return err
				}
				slog.Info("Created site", "domain", s.Domain)
			}
		}
	}

	// Create default roles
	defaultRoles := []siteModel.Role{
		{Name: "user", Description: "Regular user"},
		{Name: "moderator", Description: "Content moderator"},
		{Name: "admin", Description: "Administrator"},
	}

	for _, role := range defaultRoles {
		var existing siteModel.Role
		if err := db.First(&existing, "name = ?", role.Name).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				if err := db.Create(&role).Error; err != nil {
					slog.Error("failed to create role", "role", role.Name, "error", err)
					return err
				}
				slog.Info("Created role", "name", role.Name)
			}
		}
	}

	// Backfill: any existing OAuth client whose grants is exactly
	// ["authorization_code"] gets refresh_token added too. Without this,
	// every pre-existing client breaks the moment a refresh attempt hits
	// the grant-allowlist check we just added (15 min after any login).
	//
	// Idempotent: the JSONB equality check skips clients that already
	// have refresh_token or some other grants config. Doesn't touch
	// clients with explicit single-grant configurations other than the
	// historical default.
	res := db.Exec(`
		UPDATE oauth_clients
		SET grants = '["authorization_code","refresh_token"]'::jsonb
		WHERE grants::jsonb = '["authorization_code"]'::jsonb
	`)
	if res.Error != nil {
		// Don't fail migration — log and continue. Existing deployments
		// can recover via a manual UPDATE if needed.
		slog.Warn("failed to backfill oauth_clients.grants", "error", res.Error)
	} else if res.RowsAffected > 0 {
		slog.Info("Backfilled oauth_clients.grants with refresh_token", "rows", res.RowsAffected)
	}

	// Backfill: flip auto_consent=true for first-party clients so the
	// unified-registration redirect chain skips the consent UI on
	// kungal / moyu / wiki / ai / sticker. The column itself is added
	// by GORM AutoMigrate from siteModel.OAuthClient.AutoConsent; this
	// step only seeds the values.
	//
	// Targeted by the parent Site.Domain (resolved by JOIN), NOT by
	// client_id, so freshly-created clients on these domains in any
	// environment (dev/staging/prod) get the right default without
	// hardcoding env-specific UUIDs. Idempotent: WHERE auto_consent =
	// false ensures re-runs after admin manually toggles a row don't
	// undo their choice.
	//
	// First-party = "owned by the OAuth platform itself" = same team
	// can audit/respond to security incidents. Adding new domains here
	// means committing to keeping them secure end-to-end. Policy:
	// docs/integration/oauth/05-registration.md §auto_consent.
	firstPartyDomains := []string{
		"www.kungal.com",
		"www.moyu.moe",
		"wiki.kungal.com",
		"ai.kungal.com",
		"sticker.kungal.com",
	}
	res = db.Exec(`
		UPDATE oauth_clients
		SET auto_consent = true
		WHERE auto_consent = false
		  AND site_id IN (SELECT id FROM sites WHERE domain IN ?)
	`, firstPartyDomains)
	if res.Error != nil {
		slog.Warn("failed to backfill oauth_clients.auto_consent", "error", res.Error)
	} else if res.RowsAffected > 0 {
		slog.Info("Backfilled oauth_clients.auto_consent for first-party clients", "rows", res.RowsAffected)
	}

	return nil
}
