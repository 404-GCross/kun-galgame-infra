package main

import (
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/seed"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var retiredSchemaTables = []string{
	"galgame_series",
	"galgame_tag",
	"galgame_tag_alias",
	"galgame_official",
	"galgame_official_alias",
	"galgame_engine",
	"galgame",
	"galgame_tag_edge",
	"galgame_alias",
	"galgame_tag_relation",
	"galgame_official_relation",
	"galgame_engine_relation",
	"galgame_link",
	"galgame_cover",
	"galgame_screenshot",
	"galgame_pr",
	"galgame_revision",
	"taxonomy_revision",
	"galgame_history",
	"galgame_contributor",
	"galgame_migrations",
	"galgame_message",
	"galgame_bangumi_meta",
	"galgame_vndb_meta",
	"galgame_eg_meta",
	"galgame_dlsite_meta",
	"galgame_stats",
}

func TestCatalogMigrationMaintainsCatalogOnlySchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_DSN is required")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to the assigned test database")
	}

	// Two complete passes prove both schema and registry operations are
	// idempotent on the Catalog-only database.
	for pass := 1; pass <= 2; pass++ {
		if err := migrate.Run(db); err != nil {
			t.Fatalf("catalog migration pass %d: %v", pass, err)
		}
		if err := seed.Run(db); err != nil {
			t.Fatalf("catalog seed pass %d: %v", pass, err)
		}
	}

	for _, table := range []string{
		"catalog_medium",
		"catalog_source",
		"catalog_work",
		"catalog_work_title",
		"catalog_work_intro",
		"catalog_external_ref",
		"catalog_merge_proposal",
		"catalog_claim_event",
	} {
		t.Run("has_"+table, func(t *testing.T) {
			var present bool
			if err := db.Raw(`SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = current_schema() AND table_name = ?
			)`, table).Scan(&present).Error; err != nil {
				t.Fatalf("inspect %s: %v", table, err)
			}
			if !present {
				t.Fatalf("essential Catalog table %s is missing", table)
			}
		})
	}

	var retiredPresent []string
	if err := db.Raw(`SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name IN ?
		ORDER BY table_name`, retiredSchemaTables).Scan(&retiredPresent).Error; err != nil {
		t.Fatalf("inspect retired table family: %v", err)
	}
	if len(retiredPresent) != 0 {
		t.Fatalf("catalog migration recreated retired tables: %v", retiredPresent)
	}
}
