// Package migrate owns the kun_catalog schema migration: the AutoMigrate
// table list plus the idempotent raw-SQL section for everything AutoMigrate
// cannot express. It is called by cmd/migrate-catalog and by the catalog
// integration tests (which migrate their test database with the exact
// production schema).
package migrate

import (
	"fmt"

	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// Run applies the full catalog schema (tables + raw SQL). Idempotent: safe
// to run on every deploy and repeatedly against the same database. Seeds are
// NOT part of schema migration — call seed.Run separately.
func Run(db *gorm.DB) error {
	if err := preMigrate(db); err != nil {
		return err
	}
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
		&model.CatalogWorkLabel{},     // work↔label attribution edge (step 18)
		&model.CatalogWorkCharacter{}, // work↔character roster edge (step 45)

		// Reconciliation family (step 04).
		&model.CatalogExternalRef{},
		&model.CatalogMatchRejection{},
		&model.CatalogMatchCandidate{},
		&model.CatalogMergeProposal{},
		&model.CatalogSurvivorshipRule{},

		// Credit edges (step 05).
		&model.CatalogCredit{},
	); err != nil {
		return fmt.Errorf("catalog automigrate: %w", err)
	}
	// Editing-engine tables (E0, charter ruling 2): the edit_* family lives
	// on the catalog pool — single revision-log query surface — and rides
	// the same single migration entry point. The editing package owns its
	// DDL; catalog imports editing (platform-internal), never the reverse.
	if err := editing.AutoMigrate(db); err != nil {
		return fmt.Errorf("editing automigrate: %w", err)
	}
	return rawSQL(db)
}

// preMigrate runs BEFORE AutoMigrate: it adds NOT-NULL-without-default columns
// to already-populated tables (which AutoMigrate cannot — `ADD COLUMN … NOT
// NULL` with no default rejects a table with rows). Each block is guarded on
// the table already existing (a fresh DB has AutoMigrate create the table with
// the column NOT NULL from the model, no backfill needed) and is idempotent.
func preMigrate(db *gorm.DB) error {
	// catalog_work_character.spoiler (step 47): 0 (none) is a meaningful value,
	// so the column is NOT NULL with no default. On a table that already holds
	// step-45 roster edges, add it nullable → backfill 0 (existing Bangumi/EG
	// edges carry no spoiler) → set NOT NULL. Idempotent; skipped on a fresh DB
	// where the table does not exist yet (AutoMigrate creates it NOT NULL).
	if err := db.Exec(`
		DO $$
		BEGIN
			IF to_regclass('catalog_work_character') IS NOT NULL THEN
				ALTER TABLE catalog_work_character ADD COLUMN IF NOT EXISTS spoiler smallint;
				UPDATE catalog_work_character SET spoiler = 0 WHERE spoiler IS NULL;
				ALTER TABLE catalog_work_character ALTER COLUMN spoiler SET NOT NULL;
			END IF;
		END $$`).Error; err != nil {
		return fmt.Errorf("premigrate catalog_work_character.spoiler: %w", err)
	}
	return nil
}

// rawSQL is the post-AutoMigrate section — everything AutoMigrate cannot
// express. Every statement is idempotent, so this section reruns freely.
func rawSQL(db *gorm.DB) error {
	// (1) NFKC-folded STORED generated columns. Built while the tables are
	// empty: adding a STORED generated column later means a full table
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
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("create %s.%s_norm: %w", tc.table, tc.column, err)
		}
	}

	// (2) person → credit_name FK (the second half of the mutual reference).
	// Guarded because ADD CONSTRAINT has no IF NOT EXISTS.
	fkExists, err := constraintExists(db, "catalog_person", "fk_catalog_person_primary_credit_name")
	if err != nil {
		return err
	}
	if !fkExists {
		if err := db.Exec(`
			ALTER TABLE catalog_person
			    ADD CONSTRAINT fk_catalog_person_primary_credit_name
			    FOREIGN KEY (primary_credit_name_id) REFERENCES catalog_credit_name(id)
		`).Error; err != nil {
			return fmt.Errorf("add primary_credit_name FK: %w", err)
		}
	}

	// (3) catalog_entity_usage is a hot-update narrow table: reserve page
	// space so last_confirmed_at rewrites stay HOT (same setting as the
	// image usage table pattern). Setting the same value again is a no-op.
	if err := db.Exec(`ALTER TABLE catalog_entity_usage SET (fillfactor = 85)`).Error; err != nil {
		return fmt.Errorf("set entity_usage fillfactor: %w", err)
	}

	// (4) Table-layer CHECK constraints. ADD CONSTRAINT validates existing
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
		exists, err := constraintExists(db, cc.table, cc.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if err := db.Exec(`ALTER TABLE ` + cc.table + ` ADD CONSTRAINT ` + cc.name + ` CHECK (` + cc.expr + `)`).Error; err != nil {
			return fmt.Errorf("add check %s on %s: %w", cc.name, cc.table, err)
		}
	}

	// (5) Partial/expression unique indexes.
	for _, ix := range []struct{ name, stmt string }{
		// The exact-tier anti-squatting line (doc 10 invariant 5): one
		// external identity can be exact-linked to at most one entity per
		// entity_type. Partial — probable/related tiers coexist freely.
		{"uq_catalog_external_ref_exact", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_catalog_external_ref_exact
			    ON catalog_external_ref(source_id, external_id, entity_type)
			    WHERE link_kind = 0`},
		// One OPEN merge proposal per (entity_type, source, target) pair;
		// closed proposals (approved/executed/rejected/withdrawn) keep full
		// history.
		{"uq_catalog_merge_proposal_open", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_catalog_merge_proposal_open
			    ON catalog_merge_proposal(entity_type, source_entity_id, target_entity_id)
			    WHERE status = 0`},
		// Credit uniqueness (doc 10 §4.5): expression index because NULL
		// character_id must collide (COALESCE to 0), which a plain UNIQUE
		// would treat as distinct.
		{"uq_catalog_credit", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_catalog_credit
			    ON catalog_credit(work_id, credit_name_id, role_id, COALESCE(character_id, 0))`},
	} {
		if err := db.Exec(ix.stmt).Error; err != nil {
			return fmt.Errorf("create index %s: %w", ix.name, err)
		}
	}
	return nil
}

// constraintExists reports whether a named constraint exists on a table.
func constraintExists(db *gorm.DB, table, name string) (bool, error) {
	var exists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = ? AND conrelid = ?::regclass)`,
		name, table,
	).Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("check constraint %s on %s: %w", name, table, err)
	}
	return exists, nil
}
