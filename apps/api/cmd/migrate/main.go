package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	// Import all models
	jobsModel "api/internal/jobs/model"
	authModel "api/internal/platform/auth/model"
	"api/internal/platform/devapi"
	"api/internal/platform/permissions"
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

	// Developer-platform columns on the EXISTING oauth_clients table must be
	// added via raw SQL (NOT NULL backfilled + DROP DEFAULT) BEFORE AutoMigrate,
	// so AutoMigrate never tries to add a NOT NULL column to a populated table.
	// Idempotent. See devapi.AddOAuthClientDevColumns / 裁定 8.
	if err := devapi.AddOAuthClientDevColumns(gormDB); err != nil {
		slog.Error("failed to add developer-platform columns", "error", err)
		os.Exit(1)
	}

	// role_permission_overrides.effect, for the same reason: the overlay gained
	// its deny half on 2026-08-04 and the column is NOT NULL, so a table that
	// already holds (necessarily grant) rows must be backfilled in raw SQL
	// before AutoMigrate sees it. Idempotent.
	if err := permissions.AddOverrideEffectColumn(gormDB); err != nil {
		slog.Error("failed to add the permission-overlay effect column", "error", err)
		os.Exit(1)
	}

	// Get all models to migrate
	models := getAllModels()

	if err := gormDB.AutoMigrate(models...); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	slog.Info("Migrations completed successfully")

	// Retire the dead authz structures the permission-first migration left
	// behind (zombie permissions/role_permissions tables + user_site_data.role
	// column). Runs after AutoMigrate so it isn't undone by a recreate;
	// idempotent, so re-running migrate is a safe no-op.
	if err := dropRetiredAuthzStructures(gormDB); err != nil {
		slog.Error("failed to drop retired authz structures", "error", err)
		os.Exit(1)
	}

	// Retire the dead moderation-skeleton tables (the moderation skeleton
	// service was removed; Trust & Safety is the dedicated kun_trust DB).
	// Idempotent DROP IF EXISTS, so a re-run is a no-op.
	if err := dropRetiredModerationTables(gormDB); err != nil {
		slog.Error("failed to drop retired moderation tables", "error", err)
		os.Exit(1)
	}

	// At most one PENDING creator application per user (GORM can't express a
	// partial unique index). Backstops the service-layer "one pending" guard.
	if err := gormDB.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS uq_creator_app_pending
		ON creator_applications (user_id) WHERE status = 'pending'
	`).Error; err != nil {
		slog.Error("failed to create creator_applications partial unique index", "error", err)
		os.Exit(1)
	}

	// Case-insensitive email lookups. Login / forgot-password / existence checks
	// query LOWER(email) (email is case-insensitive in practice); this functional
	// index keeps those lookups index-backed. NON-unique on purpose: legacy data
	// has a few case-variant duplicate emails, so a unique LOWER(email) index
	// can't be created until those are deduped.
	if err := gormDB.Exec(`
		CREATE INDEX IF NOT EXISTS idx_users_email_lower ON users (LOWER(email))
	`).Error; err != nil {
		slog.Error("failed to create users LOWER(email) index", "error", err)
		os.Exit(1)
	}

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
		&authModel.UserSiteRole{},
		&authModel.UserMigration{},
		&authModel.PasswordReset{},
		&authModel.AuthorizationCode{},
		&authModel.MoemoepointLog{},
		&authModel.CreatorApplication{},
		&authModel.SigningKey{},

		// Site models
		&siteModel.Site{},
		&siteModel.OAuthClient{},
		&siteModel.Role{},

		// Developer platform (NextMoe open API): API keys + usage rollup.
		// The oauth_clients dev_* columns are handled by
		// devapi.AddOAuthClientDevColumns above (raw SQL, pre-AutoMigrate).
		&devapi.DeveloperAPIKey{},
		&devapi.DeveloperAPIUsage{},

		// NOTE: artifact models (Artifact/Manifest) moved to the dedicated
		// kun_artifacts DB — migrated by cmd/artifact's AutoMigrate, not here.
		// See docs/artifact/02-storage-and-schema.md.

		// NOTE: the moderation skeleton (moderation_jobs / moderation_results)
		// was retired with the moderation skeleton service; the dead tables
		// are dropped by
		// dropRetiredModerationTables below. Trust & Safety lives in the
		// dedicated kun_trust DB (cmd/migrate-trust).

		// Permission overlay + its audit trail (docs/auth/04 §7). The overlay is
		// ALLOW-ONLY on top of the compiled-in bundles: these tables can widen a
		// role's permissions but never cut below the code floor.
		&permissions.RolePermissionOverride{},
		&permissions.PermissionAuditLog{},

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
	// Also drop the live user_roles join table (not a model, so not in the
	// loop above). The retired role_permissions join table dies with its
	// permissions model — dropRetiredAuthzStructures handles it on the normal
	// migrate path.
	if err := db.Migrator().DropTable("user_roles"); err != nil {
		slog.Debug("drop user_roles skipped", "error", err)
	}
	return nil
}

// dropRetiredAuthzStructures removes the dead structures left by the
// permission-first authorization migration (refs step 03): the zombie
// permissions / role_permissions tables (authorization is now permission-first,
// bundled in the per-domain perm packages — no permissions table) and the dead
// per-site user_site_data.role numeric column (never read by any live
// enforcement). Idempotent — each drop is guarded, so a re-run is a no-op.
func dropRetiredAuthzStructures(db *gorm.DB) error {
	m := db.Migrator()
	// Order matters: drop the join table before the main table it references.
	if m.HasTable("role_permissions") {
		if err := m.DropTable("role_permissions"); err != nil {
			return fmt.Errorf("drop role_permissions: %w", err)
		}
		slog.Info("dropped retired table role_permissions")
	}
	if m.HasTable("permissions") {
		if err := m.DropTable("permissions"); err != nil {
			return fmt.Errorf("drop permissions: %w", err)
		}
		slog.Info("dropped retired table permissions")
	}
	// The dead per-site numeric role column (pass the DB column name — the
	// struct field is gone).
	if m.HasColumn(&authModel.UserSiteData{}, "role") {
		if err := m.DropColumn(&authModel.UserSiteData{}, "role"); err != nil {
			return fmt.Errorf("drop user_site_data.role: %w", err)
		}
		slog.Info("dropped retired column user_site_data.role")
	}
	return nil
}

// dropRetiredModerationTables removes the dead moderation-skeleton tables left
// behind by the retired moderation skeleton service (the stub provider stack, doc 02
// §8 / doc 18 P0). Trust & Safety now owns its own kun_trust database
// (cmd/migrate-trust). The two tables carried no data worth keeping (the stub
// always returned approved). Order: moderation_results references
// moderation_jobs, so drop the child first. Idempotent — guarded DROP IF
// EXISTS, so a re-run is a no-op.
func dropRetiredModerationTables(db *gorm.DB) error {
	if err := db.Exec(`DROP TABLE IF EXISTS moderation_results`).Error; err != nil {
		return fmt.Errorf("drop moderation_results: %w", err)
	}
	if err := db.Exec(`DROP TABLE IF EXISTS moderation_jobs`).Error; err != nil {
		return fmt.Errorf("drop moderation_jobs: %w", err)
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
		// creator（创作者）— trusted publisher tier between user and moderator.
		// Flat RBAC: granted ALONGSIDE user, gates publish-trust capabilities
		// (direct galgame publish incl. without a VNDB id) without any moderation
		// power. Auto-granted on contribution threshold + admin-grantable.
		// See docs/auth/01-creator-role-design.md.
		{Name: "creator", Description: "创作者 — trusted creator (direct galgame publish)"},
		{Name: "moderator", Description: "Content moderator"},
		{Name: "admin", Description: "Administrator"},
		// ren（莲）— elevated operator above admin. Flat RBAC has no
		// hierarchy, so ren is a SEPARATE role granted ALONGSIDE admin to a
		// tiny set of fully-trusted owners. It gates the genuinely dangerous
		// OAuth-admin capabilities that ordinary admins should not hold:
		//   - granting the image:upload scope to a client
		//   - flipping a client to auto_consent (silent first-party authorize)
		//   - seeing user email / IP in the admin user list & detail
		// Enforcement lives at the handlers (site_handler, admin_handler) via
		// the site/perm permission bundles (oauth.* capabilities).
		{Name: "ren", Description: "莲 — elevated operator above admin"},
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

	// Backfill: moemoepoint awarder allow-list. The service-to-service
	// POST /users/:id/moemoepoint (mint) endpoint is gated on
	// oauth_clients.moemoepoint_awarder (column added by GORM AutoMigrate from
	// siteModel.OAuthClient, default false = fail-closed). The only legitimate
	// minters are the forum + patch backends; flip them on by parent
	// Site.Domain so freshly-created clients on these domains in any
	// environment get it without hardcoding per-env client UUIDs. Idempotent:
	// WHERE moemoepoint_awarder = false skips re-runs / manual toggles.
	//
	// New content sites are deliberately NOT added here. A client that only
	// READS the balance (e.g. to seed its own local economy) must stay
	// fail-closed — minting into the shared wallet would stamp its provenance
	// onto every user's ledger. Add a domain only when that site legitimately
	// awards into the shared currency. Policy:
	// docs/integration/oauth/06-moemoepoint.md §awarder allow-list.
	awarderDomains := []string{
		"www.kungal.com",
		"www.moyu.moe",
	}
	res = db.Exec(`
		UPDATE oauth_clients
		SET moemoepoint_awarder = true
		WHERE moemoepoint_awarder = false
		  AND site_id IN (SELECT id FROM sites WHERE domain IN ?)
	`, awarderDomains)
	if res.Error != nil {
		slog.Warn("failed to backfill oauth_clients.moemoepoint_awarder", "error", res.Error)
	} else if res.RowsAffected > 0 {
		slog.Info("Backfilled oauth_clients.moemoepoint_awarder for awarder clients", "rows", res.RowsAffected)
	}

	return nil
}
