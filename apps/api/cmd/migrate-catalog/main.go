package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Init(cfg.Server.Env)
	slog.Info("connecting to catalog database", "dbname", cfg.CatalogDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("running catalog migrations...")

	if err := db.AutoMigrate(
		// Registry (vocabulary) tables, doc 17 R1. catalog_role before
		// catalog_source_role_map so the role_id FK can be created.
		&model.CatalogMedium{},
		&model.CatalogSource{},
		&model.CatalogRole{},
		&model.CatalogSourceRoleMap{},
		&model.CatalogRelationType{},

		// Entity family (step 03), in FK dependency order. person and
		// credit_name reference each other: person is created first without
		// its primary_credit_name_id FK, which the raw-SQL section adds once
		// both tables exist.
		&model.CatalogPerson{},
		&model.CatalogCreditName{},
		&model.CatalogNameAlias{},
		&model.CatalogOrg{},
		&model.CatalogLabel{},
		&model.CatalogLabelAlias{},
		&model.CatalogCharacter{},
		&model.CatalogCharacterAlias{},

		// Polymorphic infrastructure (no FKs by design).
		&model.CatalogRedirect{},
		&model.CatalogEntityUsage{},
		&model.CatalogRevision{},
	); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Post-AutoMigrate raw SQL — everything AutoMigrate cannot express.
	// Every statement is idempotent, so this section reruns freely.

	// (1) NFKC-folded STORED generated columns. Built NOW while the tables
	// are empty: adding a STORED generated column later means a full table
	// rewrite. No indexes on the norm columns yet — the consuming steps add
	// the ones their queries need. The models map these columns read-only
	// and exclude them from AutoMigrate.
	for _, tc := range []struct{ table, column string }{
		{"catalog_credit_name", "name"},
		{"catalog_name_alias", "name"},
		{"catalog_label_alias", "name"},
		{"catalog_character_alias", "name"},
		{"catalog_org", "display_name"},
		{"catalog_label", "display_name"},
		{"catalog_character", "display_name"},
	} {
		stmt := `ALTER TABLE ` + tc.table + ` ADD COLUMN IF NOT EXISTS ` + tc.column + `_norm text
			GENERATED ALWAYS AS (lower(normalize(` + tc.column + `, NFKC))) STORED`
		if err := db.DB().Exec(stmt).Error; err != nil {
			slog.Error("create norm generated column failed", "table", tc.table, "error", err)
			os.Exit(1)
		}
	}

	// (2) person → credit_name FK (the second half of the mutual reference).
	// Guarded by a pg_constraint lookup because ADD CONSTRAINT has no
	// IF NOT EXISTS.
	var fkExists bool
	if err := db.DB().Raw(`
		SELECT EXISTS (
			SELECT 1 FROM pg_constraint
			WHERE conname = 'fk_catalog_person_primary_credit_name'
			  AND conrelid = 'catalog_person'::regclass
		)
	`).Scan(&fkExists).Error; err != nil {
		slog.Error("check primary_credit_name FK failed", "error", err)
		os.Exit(1)
	}
	if !fkExists {
		if err := db.DB().Exec(`
			ALTER TABLE catalog_person
			    ADD CONSTRAINT fk_catalog_person_primary_credit_name
			    FOREIGN KEY (primary_credit_name_id) REFERENCES catalog_credit_name(id)
		`).Error; err != nil {
			slog.Error("add primary_credit_name FK failed", "error", err)
			os.Exit(1)
		}
	}

	// (3) catalog_entity_usage is a hot-update narrow table: reserve page
	// space so last_confirmed_at rewrites stay HOT (same setting as the
	// image usage table pattern). Setting the same value again is a no-op.
	if err := db.DB().Exec(`ALTER TABLE catalog_entity_usage SET (fillfactor = 85)`).Error; err != nil {
		slog.Error("set entity_usage fillfactor failed", "error", err)
		os.Exit(1)
	}

	// Seeds run on every migrate (idempotent upserts): registry vocabularies
	// are data, and this is their single write path outside manual curation.
	slog.Info("seeding catalog registries...")
	if err := seed.Run(db.DB()); err != nil {
		slog.Error("seeding failed", "error", err)
		os.Exit(1)
	}

	slog.Info("catalog migration completed successfully")
}
