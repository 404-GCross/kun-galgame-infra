package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
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

		// Work graph (step 04): registry work/title/release, then edges.
		&model.CatalogWork{},
		&model.CatalogWorkTitle{},
		&model.CatalogRelease{},
		&model.CatalogWorkRelation{},
		&model.CatalogEntityRelation{},

		// Reconciliation family (step 04).
		&model.CatalogExternalRef{},
		&model.CatalogMatchRejection{},
		&model.CatalogMatchCandidate{},
		&model.CatalogMergeProposal{},
		&model.CatalogSurvivorshipRule{},
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
		{"catalog_work_title", "title"},
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

	// (4) Table-layer CHECK constraints AutoMigrate cannot express. Each is
	// added once (pg_constraint-guarded); ADD CONSTRAINT validates existing
	// rows, which is instant on these empty/small tables.
	for _, cc := range []struct{ table, name, expr string }{
		// extra jsonb governance (doc 17 R9): object-only + 64KB cap — the
		// stock-PG stand-in for a pg_jsonschema key whitelist.
		{"catalog_work", "chk_catalog_work_extra_object", `jsonb_typeof(extra) = 'object'`},
		{"catalog_work", "chk_catalog_work_extra_size", `pg_column_size(extra) <= 65536`},
		{"catalog_release", "chk_catalog_release_extra_object", `jsonb_typeof(extra) = 'object'`},
		{"catalog_release", "chk_catalog_release_extra_size", `pg_column_size(extra) <= 65536`},
		// Relation edges never point at themselves.
		{"catalog_work_relation", "chk_catalog_work_relation_distinct", `a_work_id <> b_work_id`},
		{"catalog_entity_relation", "chk_catalog_entity_relation_distinct", `a_id <> b_id`},
		// Candidate pairs are normalized a<b — pinned at the table layer.
		{"catalog_match_candidate", "chk_catalog_match_candidate_order", `a_id < b_id`},
		// A rejection without a reason is useless: the row exists to tell
		// future importers and reviewers why the pairing is wrong.
		{"catalog_match_rejection", "chk_catalog_match_rejection_reason", `reason <> ''`},
	} {
		if err := ensureCheckConstraint(db.DB(), cc.table, cc.name, cc.expr); err != nil {
			slog.Error("add check constraint failed", "table", cc.table, "constraint", cc.name, "error", err)
			os.Exit(1)
		}
	}

	// (5) The exact-tier anti-squatting line (doc 10 invariant 5): one
	// external identity can be exact-linked to at most one entity per
	// entity_type. Partial — probable/related tiers coexist freely.
	if err := db.DB().Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS uq_catalog_external_ref_exact
		    ON catalog_external_ref(source_id, external_id, entity_type)
		    WHERE link_kind = 0
	`).Error; err != nil {
		slog.Error("create external_ref exact partial unique failed", "error", err)
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

// ensureCheckConstraint adds a named CHECK once; re-runs are no-ops
// (ADD CONSTRAINT has no IF NOT EXISTS, so existence is checked first).
func ensureCheckConstraint(db *gorm.DB, table, name, expr string) error {
	var exists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = ? AND conrelid = ?::regclass)`,
		name, table,
	).Scan(&exists).Error; err != nil {
		return err
	}
	if exists {
		return nil
	}
	return db.Exec(`ALTER TABLE ` + table + ` ADD CONSTRAINT ` + name + ` CHECK (` + expr + `)`).Error
}
