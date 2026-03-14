package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	// Import all models
	artifactModel "api/internal/platform/artifact/model"
	authModel "api/internal/platform/auth/model"
	commentModel "api/internal/platform/comment/model"
	contentModel "api/internal/platform/content/model"
	gameModel "api/internal/platform/game/model"
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

		// Site models
		&siteModel.Site{},
		&siteModel.OAuthClient{},
		&siteModel.Role{},
		&siteModel.Permission{},

		// Game models
		&gameModel.Game{},
		&gameModel.Tag{},
		// Note: game_tags join table is created automatically by GORM via many2many relation
		&gameModel.Revision{},

		// Content models
		&contentModel.Content{},

		// Comment models
		&commentModel.Comment{},

		// Artifact models
		&artifactModel.Artifact{},
		&artifactModel.Manifest{},

		// Moderation models
		&moderationModel.Job{},
		&moderationModel.Result{},
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
	// Also drop the join table
	if err := db.Migrator().DropTable("game_tags"); err != nil {
		slog.Debug("drop game_tags skipped", "error", err)
	}
	return nil
}

// seedInitialData creates initial required data
func seedInitialData(db *gorm.DB) error {
	// Create default site if not exists
	var site siteModel.Site

	// OAuth Admin site
	if err := db.First(&site, "domain = ?", "oauth.kungal.com").Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			defaultSite := &siteModel.Site{
				Name:        "KUN OAuth Admin",
				Domain:      "oauth.kungal.com",
				Description: "Central OAuth administration system",
			}
			if err := db.Create(defaultSite).Error; err != nil {
				slog.Error("failed to create default site", "error", err)
				return err
			}
			slog.Info("Created default site", "domain", defaultSite.Domain)
		}
	}

	// KUN Galgame site
	if err := db.First(&site, "domain = ?", "www.kungal.com").Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			kungalSite := &siteModel.Site{
				Name:        "KUN Galgame",
				Domain:      "www.kungal.com",
				Description: "KUN Galgame community",
			}
			if err := db.Create(kungalSite).Error; err != nil {
				slog.Error("failed to create kungal site", "error", err)
				return err
			}
			slog.Info("Created kungal site", "domain", kungalSite.Domain)
		}
	}

	// MoYu Patch site
	if err := db.First(&site, "domain = ?", "www.moyu.moe").Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			moyuSite := &siteModel.Site{
				Name:        "MoYu Patch",
				Domain:      "www.moyu.moe",
				Description: "MoYu game patches",
			}
			if err := db.Create(moyuSite).Error; err != nil {
				slog.Error("failed to create moyu site", "error", err)
				return err
			}
			slog.Info("Created moyu site", "domain", moyuSite.Domain)
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

	return nil
}
