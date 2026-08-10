package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	imgModel "api/internal/platform/image/model"
	siteModel "api/internal/platform/site/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func main() {
	seedTest := flag.Bool("seed-test-client", false, "顺带写入测试 OAuth client (kungal-test / test-secret-dev)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if err := ensureDatabaseExists(cfg.ImagesDatabase); err != nil {
		slog.Error("ensure images db", "error", err)
		os.Exit(1)
	}

	imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
	if err != nil {
		slog.Error("connect images db", "error", err)
		os.Exit(1)
	}
	defer imagesDB.Close()

	if err := imagesDB.AutoMigrate(
		&imgModel.Image{},
		&imgModel.ImageSiteUsage{},
		&imgModel.ModerationQueue{},
	); err != nil {
		slog.Error("automigrate images tables", "error", err)
		os.Exit(1)
	}
	slog.Info("images tables ready", "db", cfg.ImagesDatabase.DBName)

	oauthDB, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("connect oauth db", "error", err)
		os.Exit(1)
	}
	defer oauthDB.Close()

	if err := oauthDB.AutoMigrate(&siteModel.OAuthClient{}); err != nil {
		slog.Error("automigrate oauth_clients", "error", err)
		os.Exit(1)
	}
	slog.Info("oauth_clients image_* columns ready")

	if *seedTest {
		if err := seedTestClient(oauthDB.DB()); err != nil {
			slog.Error("seed test client", "error", err)
			os.Exit(1)
		}
		slog.Info("test client ready",
			"client_id", "kungal-test",
			"client_secret", "test-secret-dev",
			"site_key", "kungal",
			"presets", []string{"avatar", "topic", "galgame_banner"},
		)
	}

	slog.Info("image-setup done")
}

func ensureDatabaseExists(cfg config.DatabaseConfig) error {
	systemCfg := cfg
	systemCfg.DBName = "template1"

	db, err := database.OpenJobWith(systemCfg.DSN(), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	var exists bool
	if err := db.Raw(
		"SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = ?)", //nolint:misspell // datname 是 PG 系统列名
		cfg.DBName,
	).Scan(&exists).Error; err != nil {
		return err
	}
	if exists {
		slog.Info("database already exists", "name", cfg.DBName)
		return nil
	}

	if err := db.Exec(fmt.Sprintf(`CREATE DATABASE %q`, cfg.DBName)).Error; err != nil {
		return err
	}
	slog.Info("database created", "name", cfg.DBName)
	return nil
}

func seedTestClient(db *gorm.DB) error {
	allowedPresets, _ := datatypes.JSON(`["avatar","topic","galgame_banner","message"]`).MarshalJSON()
	emptyJSON, _ := datatypes.JSON(`[]`).MarshalJSON()

	row := &siteModel.OAuthClient{
		ID:                   "kungal-test",
		Name:                 "kungal-test",
		Secret:               "test-secret-dev",
		RedirectURIs:         emptyJSON,
		Grants:               emptyJSON,
		ImageEnabled:         true,
		ImageSiteKey:         "kungal",
		ImageQuotaDaily:      10000,
		ImageQuotaBytesDaily: 10737418240,
		ImageMaxFileSize:     10485760,
		ImageAllowedPresets:  allowedPresets,
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "secret", "image_enabled", "image_site_key",
			"image_quota_daily", "image_quota_bytes_daily",
			"image_max_file_size", "image_allowed_presets",
		}),
	}).Create(row).Error
}
