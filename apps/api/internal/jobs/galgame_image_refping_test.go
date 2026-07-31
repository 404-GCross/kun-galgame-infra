package jobs

// The refping collector, after wave 161 removed its wiki lane.
//
// What is left to test is the ENGINE lane: hashes that live only inside an edit
// snapshot or an open proposal. That is the whole remaining point of the job —
// a hash a live catalog_work_cover row still references is kept alive by
// catalog-image-refping, but one that only a years-old revision mentions has no
// other keeper, and a revert of that revision must still be able to fetch it.
//
// Requires TEST_DATABASE_DSN pointing at a database carrying the engine tables
// (edit_revision / edit_proposal); unset or absent → skipped.

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// dsnEnv resolves the TEST_DATABASE_DSN used elsewhere in the suite.
// Empty → tests are skipped (CI without a DB still passes).
func dsnEnv(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	return dsn
}

// openEditTestDB connects and clears the two engine tables this file touches.
// It deliberately does NOT AutoMigrate: the engine schema belongs to
// cmd/migrate-catalog, and a second definition of it here is exactly how the
// two drift apart.
func openEditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsnEnv(t)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	var present bool
	require.NoError(t, db.Raw(`SELECT to_regclass('edit_revision') IS NOT NULL`).Scan(&present).Error)
	if !present {
		t.Skip("TEST_DATABASE_DSN has no engine tables")
	}
	require.NoError(t, db.Exec(`TRUNCATE edit_revision, edit_proposal RESTART IDENTITY CASCADE`).Error)
	return db
}

// hash constants (sha-256 = 64 hex chars).
const (
	hRev    = "6666666666666666666666666666666666666666666666666666666666666666"
	hPatch  = "7777777777777777777777777777777777777777777777777777777777777777"
	hLegacy = "8888888888888888888888888888888888888888888888888888888888888888"
	hShot   = "9999999999999999999999999999999999999999999999999999999999999999"
)

// TestRefpingEngineLaneMatchesBothKeySpellings is the property wave 161's
// edit-history rekey makes load-bearing.
//
// The rekey rewrites every galgame.game row in these tables to catalog.work
// with catalog field keys. A collector filtered on galgame.game alone would
// return the empty set the instant that migration lands — and since the job
// treats "0 hashes kept alive" as a failed run, the symptom would not be
// missing bytes but a daily alert: a much slower way to discover that every
// historical image reference had stopped being protected.
func TestRefpingEngineLaneMatchesBothKeySpellings(t *testing.T) {
	db := openEditTestDB(t)
	ctx := context.Background()

	// Pre-rekey shape: galgame.game carrying the wiki field keys.
	require.NoError(t, db.Exec(`
		INSERT INTO edit_revision
			(entity_family, entity_type, entity_id, seq, action, changed_fields, snapshot, actor_uid, site, created_at)
		VALUES ('galgame', 'galgame.game', 1, 1, 0, '[]'::jsonb, ?::jsonb, 1, 'kungal', now())`,
		`{"galgame.game.covers":[{"image_hash":"`+hRev+`"}]}`).Error)

	// Post-rekey shape: catalog.work with the catalog field keys — the SAME
	// history, one migration later.
	require.NoError(t, db.Exec(`
		INSERT INTO edit_revision
			(entity_family, entity_type, entity_id, seq, action, changed_fields, snapshot, actor_uid, site, created_at)
		VALUES ('catalog', 'catalog.work', 2, 1, 0, '[]'::jsonb, ?::jsonb, 1, 'kungal', now())`,
		`{"catalog.work.screenshots":[{"image_hash":"`+hShot+`"}]}`).Error)

	// An OPEN proposal's proposed image: until somebody decides it, nothing
	// else in the database references those bytes at all.
	require.NoError(t, db.Exec(`
		INSERT INTO edit_proposal
			(entity_family, entity_type, entity_id, base_revision_seq, patch, proposer_uid, note, site, status, decision_note, created_at, updated_at)
		VALUES ('catalog', 'catalog.work', 3, 0, ?::jsonb, 1, '', 'kungal', 0, '', now(), now())`,
		`{"catalog.work.covers":[{"image_hash":"`+hPatch+`"}]}`).Error)

	// The archived old-wire snapshot keeps its BARE keys — the rekey does not
	// rewrite legacy_meta, so the collector must not expect a prefix there.
	require.NoError(t, db.Exec(`
		INSERT INTO edit_proposal
			(entity_family, entity_type, entity_id, base_revision_seq, patch, legacy_meta, proposer_uid, note, site, status, decision_note, created_at, updated_at)
		VALUES ('galgame', 'galgame.game', 4, 0, '{}'::jsonb, ?::jsonb, 1, '', 'kungal', 1, '', now(), now())`,
		`{"snapshot":{"covers":[{"image_hash":"`+hLegacy+`"}]}}`).Error)

	got, err := collectEditRefpingHashes(ctx, db)
	require.NoError(t, err)
	set := make(map[string]bool, len(got))
	for _, h := range got {
		set[h] = true
	}
	assert.True(t, set[hRev], "pre-rekey revision hash")
	assert.True(t, set[hShot], "post-rekey revision hash")
	assert.True(t, set[hPatch], "open proposal's proposed hash")
	assert.True(t, set[hLegacy], "archived legacy snapshot hash")
	assert.Len(t, got, 4, "no duplicates, nothing invented")
}

// TestRefpingEngineLaneSkipsOtherFamilies: the collector stays scoped to the
// two entity types whose images were uploaded under the galgame_wiki image
// site. Widening it would make the job ping hashes it holds no credential for,
// and a 404 storm is indistinguishable from the site misconfiguration the
// zero-guard exists to catch.
func TestRefpingEngineLaneSkipsOtherFamilies(t *testing.T) {
	db := openEditTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.Exec(`
		INSERT INTO edit_revision
			(entity_family, entity_type, entity_id, seq, action, changed_fields, snapshot, actor_uid, site, created_at)
		VALUES ('catalog', 'catalog.character', 9, 1, 0, '[]'::jsonb, ?::jsonb, 1, 'kungal', now())`,
		`{"catalog.work.covers":[{"image_hash":"`+hRev+`"}]}`).Error)

	got, err := collectEditRefpingHashes(ctx, db)
	require.NoError(t, err)
	assert.Empty(t, got)
}
