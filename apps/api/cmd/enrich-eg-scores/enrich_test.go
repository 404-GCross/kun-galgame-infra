package main

import (
	"os"
	"testing"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The tool spans three databases in production (wiki / catalog / EG mirror). In
// the test all three point at ONE schema-isolated fixture in the test DB: a
// dedicated schema on a single pooled connection with SET search_path, so the
// tool's unqualified table names resolve to the minimal fixture tables and
// never collide with other packages' public-schema tables. The schema is
// dropped at the end.
func openFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1) // pin one connection so SET search_path sticks

	require.NoError(t, db.Exec(`DROP SCHEMA IF EXISTS egscores_test CASCADE`).Error)
	require.NoError(t, db.Exec(`CREATE SCHEMA egscores_test`).Error)
	require.NoError(t, db.Exec(`SET search_path TO egscores_test`).Error)

	// Minimal fixture shapes (only the columns the join + upsert touch).
	for _, ddl := range []string{
		`CREATE TABLE games (id int PRIMARY KEY, median int, count2 int)`,
		`CREATE TABLE catalog_external_ref (
			entity_type smallint, entity_id bigint, source_id smallint,
			external_id text, link_kind smallint)`,
		`CREATE TABLE catalog_work (
			id bigint PRIMARY KEY, site text, product_work_id bigint, deleted_at timestamptz)`,
		`CREATE TABLE galgame (id int PRIMARY KEY)`,
		`CREATE TABLE galgame_eg_meta (
			galgame_id int PRIMARY KEY, eg_game_id int NOT NULL, median int,
			vote_count int NOT NULL, synced_at timestamptz NOT NULL)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS egscores_test CASCADE`) })
	return db
}

// anchor writes one catalog_work + its EG external_ref. claimed=false leaves
// product_work_id NULL (unclaimed); site defaults to galgame_wiki unless given.
func anchor(t *testing.T, db *gorm.DB, workID int64, egID string, productWorkID *int64, linkKind int16, site string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_work (id, site, product_work_id, deleted_at) VALUES (?, ?, ?, NULL)`,
		workID, site, productWorkID).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind)
		 VALUES (?, ?, ?, ?, ?)`,
		entityTypeWork, workID, egSourceID, egID, linkKind).Error)
}

func egGame(t *testing.T, db *gorm.DB, id int, median *int, count2 *int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO games (id, median, count2) VALUES (?, ?, ?)`, id, median, count2).Error)
}

func galgame(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame (id) VALUES (?)`, id).Error)
}

func p(v int) *int      { return &v }
func pw(v int64) *int64 { return &v }

func TestEnrichEGScenarios(t *testing.T) {
	db := openFixture(t)

	// #1 happy path: exact + claimed + EG present + galgame present → written.
	anchor(t, db, 1, "1001", pw(101), linkKindExact, wikiSite)
	egGame(t, db, 1001, p(80), p(120))
	galgame(t, db, 101)
	// #2 probable anchor (link_kind=1) → NOT taken.
	anchor(t, db, 2, "1002", pw(102), 1 /* probable */, wikiSite)
	egGame(t, db, 1002, p(70), p(50))
	galgame(t, db, 102)
	// #3 unclaimed work (product_work_id NULL) → NOT taken.
	anchor(t, db, 3, "1003", nil, linkKindExact, wikiSite)
	egGame(t, db, 1003, p(60), p(40))
	// #4 wrong site tenant → NOT taken.
	anchor(t, db, 4, "1004", pw(104), linkKindExact, "some_other_site")
	egGame(t, db, 1004, p(65), p(30))
	galgame(t, db, 104)
	// #5 EG median NULL → row written with NULL median (never a fake zero).
	anchor(t, db, 5, "1005", pw(105), linkKindExact, wikiSite)
	egGame(t, db, 1005, nil, p(3))
	galgame(t, db, 105)
	// #6 galgame absent from wiki → skipped_no_galgame, no row.
	anchor(t, db, 6, "1006", pw(106), linkKindExact, wikiSite)
	egGame(t, db, 1006, p(55), p(20))
	// (no galgame row 106)
	// #7 EG game absent from mirror → missing_in_mirror.
	anchor(t, db, 7, "1007", pw(107), linkKindExact, wikiSite)
	galgame(t, db, 107)
	// (no games row 1007)
	// #8 multi-anchor: one work with two EG exact refs → collapse, pick most-voted.
	anchor(t, db, 8, "1008", pw(108), linkKindExact, wikiSite)
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind)
		 VALUES (?, ?, ?, ?, ?)`, entityTypeWork, 8, egSourceID, "1018", linkKindExact).Error)
	egGame(t, db, 1008, p(40), p(10))  // fewer votes
	egGame(t, db, 1018, p(90), p(999)) // most-voted → chosen
	galgame(t, db, 108)

	// Dry run first: resolves + counts, writes nothing.
	dry, err := Run(db, db, db, Options{Apply: false})
	require.NoError(t, err)
	assertCounts(t, dry)
	var n int64
	require.NoError(t, db.Table("galgame_eg_meta").Count(&n).Error)
	assert.Zero(t, n, "dry run must not write")

	// Apply.
	st, err := Run(db, db, db, Options{Apply: true})
	require.NoError(t, err)
	assertCounts(t, st)
	require.NoError(t, db.Table("galgame_eg_meta").Count(&n).Error)
	assert.Equal(t, int64(3), n, "written rows = #1 #5 #8 (#6 no-galgame, #7 no-mirror excluded)")

	// #1 value fidelity.
	var m1 model.GalgameEGMeta
	require.NoError(t, db.First(&m1, "galgame_id = ?", 101).Error)
	assert.Equal(t, 1001, m1.EGGameID)
	require.NotNil(t, m1.Median)
	assert.Equal(t, 80, *m1.Median)
	assert.Equal(t, 120, m1.VoteCount)

	// #5 NULL median semantics.
	var m5 model.GalgameEGMeta
	require.NoError(t, db.First(&m5, "galgame_id = ?", 105).Error)
	assert.Nil(t, m5.Median, "EG median NULL must persist as NULL, not 0")
	assert.Equal(t, 3, m5.VoteCount)

	// #8 multi-anchor picked the most-voted EG game (1018).
	var m8 model.GalgameEGMeta
	require.NoError(t, db.First(&m8, "galgame_id = ?", 108).Error)
	assert.Equal(t, 1018, m8.EGGameID)
	require.NotNil(t, m8.Median)
	assert.Equal(t, 90, *m8.Median)
	assert.Equal(t, 999, m8.VoteCount)

	// Idempotency: a second apply UPDATEs in place, no row growth.
	st2, err := Run(db, db, db, Options{Apply: true})
	require.NoError(t, err)
	assertCounts(t, st2)
	require.NoError(t, db.Table("galgame_eg_meta").Count(&n).Error)
	assert.Equal(t, int64(3), n, "second apply must not add rows")
}

func assertCounts(t *testing.T, s *Stats) {
	t.Helper()
	// Anchors = exact+galgame_wiki+claimed join rows: #1 #5 #6 #7 + two for #8 = 6.
	assert.Equal(t, 6, s.Anchors, "probable/unclaimed/wrong-site excluded from anchors")
	assert.Equal(t, 1, s.MultiAnchor, "#8's second anchor collapsed")
	assert.Equal(t, 4, s.Matched, "#1 #5 #6 #8 have an EG game in the mirror (#7 missing)")
	assert.Equal(t, 3, s.Written, "#1 #5 #8 (#6 galgame absent)")
	assert.Equal(t, 1, s.MissingInMirror, "#7 EG game absent from mirror")
	assert.Equal(t, 1, s.SkippedNoGalgame, "#6 target galgame absent from wiki")
}
