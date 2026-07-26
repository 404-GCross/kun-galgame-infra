package storerefs

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test: catalog Gold schema + a minimal EG mirror fixture in its
// OWN schema (storerefs_eg) via a search_path DSN (the workratings pattern).
var (
	testDB    *gorm.DB
	testDSN   string
	egTestDSN string
)

func TestMain(m *testing.M) {
	testDSN = os.Getenv("TEST_DATABASE_DSN")
	if testDSN == "" {
		testDSN = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	for _, ddl := range []string{
		`CREATE SCHEMA IF NOT EXISTS storerefs_eg`,
		`CREATE TABLE IF NOT EXISTS storerefs_eg.games (id bigint PRIMARY KEY, steam bigint, dmm text)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: mirror fixture failed: %v\n", err)
			os.Exit(0)
		}
	}
	egTestDSN = testDSN + " options='-csearch_path=storerefs_eg'"
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_match_rejection", "catalog_external_ref", "catalog_work", "storerefs_eg.games",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func sourceID(t *testing.T, key string) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error)
	require.NotZero(t, id, "source %s must be seeded", key)
	return id
}

func mediumID(t *testing.T) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

func mkWork(t *testing.T, medium int16, name string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkAnchor(t *testing.T, workID int64, externalID string, source int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: source,
		ExternalID: externalID, LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
	}).Error)
}

// TestImportStoreRefs pins: both lanes plan/write, dmm seed row exists (id 15),
// probable grade + rule tags, negative knowledge blocks, InsertRefIfAbsent
// never re-grades, second apply writes zero.
func TestImportStoreRefs(t *testing.T) {
	clean(t)
	medium := mediumID(t)
	egSrc := sourceID(t, "erogamespace")
	steamSrc := sourceID(t, "steam")
	dmmSrc := sourceID(t, "dmm") // the step-91 seed row — resolvable or the test fails

	wA := mkWork(t, medium, "both-stores")
	wB := mkWork(t, medium, "dmm-rejected")
	wC := mkWork(t, medium, "steam-preexisting")
	mkAnchor(t, wA, "201", egSrc)
	mkAnchor(t, wB, "202", egSrc)
	mkAnchor(t, wC, "203", egSrc)
	require.NoError(t, testDB.Exec(`INSERT INTO storerefs_eg.games (id, steam, dmm) VALUES
		(201, 4710010, '1564apc14970'), (202, NULL, 'elf_0035'), (203, 1689910, NULL)`).Error)

	// Negative knowledge: wB × dmm elf_0035 was human-rejected.
	require.NoError(t, testDB.Create(&model.CatalogMatchRejection{
		EntityType: model.EntityTypeWork, EntityID: wB, SourceID: dmmSrc,
		ExternalID: "elf_0035", Reason: "test rejection",
	}).Error)
	// Pre-existing assertion: wC × steam already EXACT (human-verified, say).
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: wC, SourceID: steamSrc,
		ExternalID: "1689910", LinkKind: model.LinkKindExact, MatchedBy: "human:1",
	}).Error)

	ctx := context.Background()
	opts := Opts{DSN: testDSN, EGDSN: egTestDSN}

	// Dry.
	st, err := Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 3, st.Anchored)
	assert.Equal(t, 2, st.SteamPlanned, "A + C (existence is discovered at write time)")
	assert.Equal(t, 1, st.DmmPlanned, "A only — B is rejection-blocked")
	assert.Equal(t, 1, st.Rejected)
	assert.Zero(t, st.SteamWritten+st.DmmWritten)

	// Apply.
	opts.Apply = true
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, st.SteamWritten, "A written; C already asserted")
	assert.Equal(t, 1, st.SteamExists, "C skipped — never re-grade")
	assert.Equal(t, 1, st.DmmWritten)
	assert.Zero(t, st.Errors)

	var ref model.CatalogExternalRef
	require.NoError(t, testDB.Where(
		"entity_type = ? AND entity_id = ? AND source_id = ?", model.EntityTypeWork, wA, steamSrc).First(&ref).Error)
	assert.Equal(t, model.LinkKindProbable, ref.LinkKind)
	assert.Equal(t, "rule:eg-steam", ref.MatchedBy)
	ref = model.CatalogExternalRef{} // fresh struct per First — GORM reuses populated PKs as conditions
	require.NoError(t, testDB.Where(
		"entity_type = ? AND entity_id = ? AND source_id = ?", model.EntityTypeWork, wA, dmmSrc).First(&ref).Error)
	assert.Equal(t, "1564apc14970", ref.ExternalID)
	assert.Equal(t, "rule:eg-dmm", ref.MatchedBy)
	// wC's steam ref kept its original grade.
	ref = model.CatalogExternalRef{}
	require.NoError(t, testDB.Where(
		"entity_type = ? AND entity_id = ? AND source_id = ?", model.EntityTypeWork, wC, steamSrc).First(&ref).Error)
	assert.Equal(t, model.LinkKindExact, ref.LinkKind, "existing assertion never re-graded")
	// wB has NO dmm ref (rejection honored).
	err = testDB.Where("entity_type = ? AND entity_id = ? AND source_id = ?",
		model.EntityTypeWork, wB, dmmSrc).First(&model.CatalogExternalRef{}).Error
	assert.Error(t, err)

	// Second apply: zero writes.
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Zero(t, st.SteamWritten+st.DmmWritten)
	assert.Equal(t, 2, st.SteamExists)
	assert.Equal(t, 1, st.DmmExists)
}
