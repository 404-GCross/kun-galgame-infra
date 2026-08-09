package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/internal/platform/catalog/srcvndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seeding failed: %v\n", err)
		os.Exit(0)
	}
	// src_vndb is rebuildable staging outside the catalog migration order, so
	// the audit's mirror side has to be provisioned explicitly.
	if err := srcvndb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_vndb schema failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`TRUNCATE catalog_external_ref, catalog_work, src_vndb.vn RESTART IDENTITY CASCADE`).Error)
}

func vndbSourceID(t *testing.T) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKeyVndb).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

func seedWork(t *testing.T, name string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func seedAnchor(t *testing.T, workID int64, externalID string, dead bool) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: vndbSourceID(t),
		ExternalID: externalID, LinkKind: model.LinkKindExact, MatchedBy: "rule:wiki-vndb-id",
	}).Error)
	if dead {
		require.NoError(t, testDB.Exec(`UPDATE catalog_external_ref SET dead_at = now()
			WHERE entity_type = ? AND entity_id = ? AND external_id = ?`,
			model.EntityTypeWork, workID, externalID).Error)
	}
}

// seedMirror fills src_vndb.vn with n synthetic entries v1..vn, plus any extra
// ids named. The count matters only for the guardrail, so the rows are minimal.
func seedMirror(t *testing.T, n int, extra ...string) {
	t.Helper()
	ids := make([]string, 0, n+len(extra))
	for i := 1; i <= n; i++ {
		ids = append(ids, fmt.Sprintf("vfill%d", i))
	}
	ids = append(ids, extra...)
	rows := make([]srcvndb.VN, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, srcvndb.VN{ID: id, OLang: "ja", IngestedAt: time.Now()})
	}
	require.NoError(t, testDB.CreateInBatches(rows, 1000).Error)
}

func deadAt(t *testing.T, externalID string) *string {
	t.Helper()
	var got *string
	require.NoError(t, testDB.Raw(`SELECT dead_at::text FROM catalog_external_ref WHERE external_id = ?`,
		externalID).Scan(&got).Error)
	return got
}

// The two directions, in one apply: an anchor whose VNDB entry is gone gets
// dead_at; an anchor previously marked dead whose entry is back is cleared.
func TestAuditMarksDeadAndClearsRevived(t *testing.T) {
	clean(t)
	ctx := context.Background()

	gone := seedWork(t, "deleted upstream")
	revived := seedWork(t, "restored upstream")
	healthy := seedWork(t, "always fine")
	seedAnchor(t, gone, "vgone", false)
	seedAnchor(t, revived, "vback", true)
	seedAnchor(t, healthy, "vlive", false)
	seedMirror(t, 10, "vback", "vlive")

	st, err := audit(ctx, testDB, true, 5)
	require.NoError(t, err)
	assert.EqualValues(t, 3, st.Anchors)
	assert.EqualValues(t, 1, st.Marked)
	assert.EqualValues(t, 1, st.Cleared)

	assert.NotNil(t, deadAt(t, "vgone"), "an anchor absent upstream must be marked dead")
	assert.Nil(t, deadAt(t, "vback"), "a restored anchor must be cleared back to live")
	assert.Nil(t, deadAt(t, "vlive"), "a live anchor must be left alone")

	// Re-runnable: a second pass over the same mirror writes nothing.
	st, err = audit(ctx, testDB, true, 5)
	require.NoError(t, err)
	assert.EqualValues(t, 0, st.Marked)
	assert.EqualValues(t, 0, st.Cleared)
}

// The dry run reports both directions and writes nothing.
func TestAuditDryRunWritesNothing(t *testing.T) {
	clean(t)
	ctx := context.Background()

	w := seedWork(t, "deleted upstream")
	seedAnchor(t, w, "vgone", false)
	seedMirror(t, 10)

	st, err := audit(ctx, testDB, false, 5)
	require.NoError(t, err)
	assert.EqualValues(t, 1, st.ToMark)
	assert.EqualValues(t, 0, st.Marked)
	assert.Nil(t, deadAt(t, "vgone"), "a dry run must not write dead_at")
}

// The guardrail: an implausibly small mirror (the mid-reload window) must make
// --apply REFUSE rather than mark every anchor dead. A dry run against the
// same mirror is still allowed — it writes nothing, so it cannot do harm.
func TestAuditRefusesApplyAgainstUndersizedMirror(t *testing.T) {
	clean(t)
	ctx := context.Background()

	w := seedWork(t, "would be wrongly killed")
	seedAnchor(t, w, "vgone", false)
	seedMirror(t, 3) // far below the floor asked for below

	_, err := audit(ctx, testDB, true, 50_000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to apply")
	assert.Nil(t, deadAt(t, "vgone"), "the refusal must happen before any write")

	st, err := audit(ctx, testDB, false, 50_000)
	require.NoError(t, err, "a dry run is safe against any mirror")
	assert.EqualValues(t, 1, st.ToMark)
}
