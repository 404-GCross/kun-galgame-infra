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

// The tool spans three databases in production (wiki / catalog / DLsite
// mirror). In the test all three point at ONE schema-isolated fixture in the
// test DB (the enrich-eg-scores harness verbatim): a dedicated schema on a
// single pooled connection with SET search_path, dropped at the end.
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

	require.NoError(t, db.Exec(`DROP SCHEMA IF EXISTS dlsitemeta_test CASCADE`).Error)
	require.NoError(t, db.Exec(`CREATE SCHEMA dlsitemeta_test`).Error)
	require.NoError(t, db.Exec(`SET search_path TO dlsitemeta_test`).Error)

	// Minimal fixture shapes (only the columns the join + upsert touch).
	for _, ddl := range []string{
		`CREATE TABLE works (workno text PRIMARY KEY, info_json jsonb)`,
		`CREATE TABLE catalog_source (id smallint PRIMARY KEY, key text NOT NULL)`,
		`CREATE TABLE catalog_medium (id smallint PRIMARY KEY, key text NOT NULL)`,
		`CREATE TABLE catalog_external_ref (
			entity_type smallint, entity_id bigint, source_id smallint,
			external_id text, link_kind smallint)`,
		`CREATE TABLE catalog_release (id bigint PRIMARY KEY, work_id bigint, deleted_at timestamptz)`,
		`CREATE TABLE catalog_work (
			id bigint PRIMARY KEY, medium_id smallint, site text, product_work_id bigint, deleted_at timestamptz)`,
		`CREATE TABLE galgame (id int PRIMARY KEY)`,
		`CREATE TABLE galgame_dlsite_meta (
			galgame_id int PRIMARY KEY, workno text NOT NULL,
			rate_average_star numeric, rate_count int,
			dl_count bigint, wishlist_count bigint, review_count int,
			synced_at timestamptz NOT NULL)`,
		// Registry seeds — the tool resolves both by key at runtime.
		`INSERT INTO catalog_source (id, key) VALUES (4, 'dlsite')`,
		`INSERT INTO catalog_medium (id, key) VALUES (1, 'galgame'), (5, 'asmr')`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS dlsitemeta_test CASCADE`) })
	return db
}

// anchor writes one catalog_work + release + its DLsite release-level ref.
func anchor(t *testing.T, db *gorm.DB, workID int64, workno string, productWorkID *int64, linkKind, medium int16, site string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_work (id, medium_id, site, product_work_id, deleted_at) VALUES (?, ?, ?, ?, NULL)`,
		workID, medium, site, productWorkID).Error)
	relID := workID*10 + 1
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_release (id, work_id, deleted_at) VALUES (?, ?, NULL)`, relID, workID).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind)
		 VALUES (?, ?, 4, ?, ?)`,
		entityTypeRelease, relID, workno, linkKind).Error)
}

// extraAnchor attaches a second workno to an existing work via a second release.
func extraAnchor(t *testing.T, db *gorm.DB, workID int64, workno string) {
	t.Helper()
	relID := workID*10 + 2
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_release (id, work_id, deleted_at) VALUES (?, ?, NULL)`, relID, workID).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind)
		 VALUES (?, ?, 4, ?, ?)`,
		entityTypeRelease, relID, workno, linkKindExact).Error)
}

// mirrorWork writes one DLsite mirror row; nil pointers leave the key out of
// info_json entirely (the real mirror's "not published" shape).
func mirrorWork(t *testing.T, db *gorm.DB, workno string, star *float64, rc *int, dl, wl *int64, rv *int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO works (workno, info_json) VALUES (?, jsonb_strip_nulls(jsonb_build_object(
		'rate_average_2dp', ?::float8, 'rate_count', ?::int,
		'dl_count', ?::bigint, 'wishlist_count', ?::bigint, 'review_count', ?::int)))`,
		workno, star, rc, dl, wl, rv).Error)
}

func galgame(t *testing.T, db *gorm.DB, id int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame (id) VALUES (?)`, id).Error)
}

func pi(v int) *int         { return &v }
func pf(v float64) *float64 { return &v }
func pl(v int64) *int64     { return &v }
func pw(v int64) *int64     { return &v }

func TestEnrichDlsiteScenarios(t *testing.T) {
	db := openFixture(t)

	// #1 happy path: exact + claimed + mirror rated → full row.
	anchor(t, db, 1, "RJ000101", pw(101), linkKindExact, 1, wikiSite)
	mirrorWork(t, db, "RJ000101", pf(4.36), pi(120), pl(2000), pl(300), pi(12))
	galgame(t, db, 101)
	// #2 probable anchor → NOT taken.
	anchor(t, db, 2, "RJ000102", pw(102), 1 /* probable */, 1, wikiSite)
	mirrorWork(t, db, "RJ000102", pf(4.0), pi(10), pl(1), pl(1), pi(1))
	galgame(t, db, 102)
	// #3 unclaimed work → NOT taken.
	anchor(t, db, 3, "RJ000103", nil, linkKindExact, 1, wikiSite)
	mirrorWork(t, db, "RJ000103", pf(4.0), pi(10), pl(1), pl(1), pi(1))
	// #4 ASMR medium → NOT taken (game-domain ruling).
	anchor(t, db, 4, "RJ000104", pw(104), linkKindExact, 5, wikiSite)
	mirrorWork(t, db, "RJ000104", pf(4.9), pi(999), pl(9), pl(9), pi(9))
	galgame(t, db, 104)
	// #5 unrated mirror row (no rate keys) → row written with NULL rating but
	// live popularity counters (the claimed popularity bridge needs them).
	anchor(t, db, 5, "RJ000105", pw(105), linkKindExact, 1, wikiSite)
	mirrorWork(t, db, "RJ000105", nil, nil, pl(50), pl(7), pi(0))
	galgame(t, db, 105)
	// #6 galgame absent from wiki → skipped_no_galgame, no row.
	anchor(t, db, 6, "RJ000106", pw(106), linkKindExact, 1, wikiSite)
	mirrorWork(t, db, "RJ000106", pf(3.5), pi(30), pl(10), pl(2), pi(1))
	// (no galgame row 106)
	// #7 workno absent from mirror → missing_in_mirror.
	anchor(t, db, 7, "RJ000107", pw(107), linkKindExact, 1, wikiSite)
	galgame(t, db, 107)
	// #8 multi-anchor: two worknos on one work → most-rated wins.
	anchor(t, db, 8, "RJ000108", pw(108), linkKindExact, 1, wikiSite)
	extraAnchor(t, db, 8, "RJ000118")
	mirrorWork(t, db, "RJ000108", pf(3.0), pi(5), pl(100), pl(1), pi(0))     // fewer ratings
	mirrorWork(t, db, "RJ000118", pf(4.8), pi(500), pl(9000), pl(50), pi(3)) // most-rated → chosen
	galgame(t, db, 108)
	// #9 corrupt negative counter → normalized to NULL, row still written.
	anchor(t, db, 9, "RJ000109", pw(109), linkKindExact, 1, wikiSite)
	mirrorWork(t, db, "RJ000109", nil, nil, pl(-4), pl(3), pi(0))
	galgame(t, db, 109)

	// Dry run first: resolves + counts, writes nothing.
	dry, err := Run(db, db, db, Options{Apply: false})
	require.NoError(t, err)
	assertCounts(t, dry, false)
	var n int64
	require.NoError(t, db.Table("galgame_dlsite_meta").Count(&n).Error)
	assert.Zero(t, n, "dry run must not write")

	// Apply.
	st, err := Run(db, db, db, Options{Apply: true})
	require.NoError(t, err)
	assertCounts(t, st, false)
	require.NoError(t, db.Table("galgame_dlsite_meta").Count(&n).Error)
	assert.Equal(t, int64(4), n, "written rows = #1 #5 #8 #9 (#6 no-galgame, #7 no-mirror excluded)")

	// #1 value fidelity (star stays on the native 0-5 scale, 2dp precision).
	var m1 model.GalgameDlsiteMeta
	require.NoError(t, db.First(&m1, "galgame_id = ?", 101).Error)
	assert.Equal(t, "RJ000101", m1.Workno)
	require.NotNil(t, m1.RateAverageStar)
	assert.InDelta(t, 4.36, *m1.RateAverageStar, 1e-9)
	require.NotNil(t, m1.RateCount)
	assert.Equal(t, 120, *m1.RateCount)
	require.NotNil(t, m1.DlCount)
	assert.EqualValues(t, 2000, *m1.DlCount)
	require.NotNil(t, m1.WishlistCount)
	assert.EqualValues(t, 300, *m1.WishlistCount)
	require.NotNil(t, m1.ReviewCount)
	assert.Equal(t, 12, *m1.ReviewCount)

	// #5 NULL rating + live counters (review_count 0 is a real value).
	var m5 model.GalgameDlsiteMeta
	require.NoError(t, db.First(&m5, "galgame_id = ?", 105).Error)
	assert.Nil(t, m5.RateAverageStar, "unrated must persist as NULL, not 0")
	assert.Nil(t, m5.RateCount)
	require.NotNil(t, m5.DlCount)
	assert.EqualValues(t, 50, *m5.DlCount)
	require.NotNil(t, m5.ReviewCount)
	assert.Equal(t, 0, *m5.ReviewCount)

	// #8 multi-anchor picked the most-rated workno.
	var m8 model.GalgameDlsiteMeta
	require.NoError(t, db.First(&m8, "galgame_id = ?", 108).Error)
	assert.Equal(t, "RJ000118", m8.Workno)
	require.NotNil(t, m8.RateCount)
	assert.Equal(t, 500, *m8.RateCount)

	// #9 negative dl_count normalized to NULL; wishlist survives.
	var m9 model.GalgameDlsiteMeta
	require.NoError(t, db.First(&m9, "galgame_id = ?", 109).Error)
	assert.Nil(t, m9.DlCount, "corrupt negative counter never lands")
	require.NotNil(t, m9.WishlistCount)
	assert.EqualValues(t, 3, *m9.WishlistCount)

	// Idempotency: a second apply is a change-detected NO-OP — zero effective
	// writes, everything unchanged, no row growth, synced_at untouched.
	before := m1.SyncedAt
	st2, err := Run(db, db, db, Options{Apply: true})
	require.NoError(t, err)
	assertCounts(t, st2, true)
	require.NoError(t, db.Table("galgame_dlsite_meta").Count(&n).Error)
	assert.Equal(t, int64(4), n, "second apply must not add rows")
	require.NoError(t, db.First(&m1, "galgame_id = ?", 101).Error)
	assert.Equal(t, before.UTC(), m1.SyncedAt.UTC(), "unchanged row keeps its synced_at (change-detected upsert)")

	// Refresh loop: mutate one mirror value → third apply updates exactly that
	// row (Written=1, the rest unchanged).
	require.NoError(t, db.Exec(
		`UPDATE works SET info_json = jsonb_set(info_json, '{wishlist_count}', '301') WHERE workno = 'RJ000101'`).Error)
	st3, err := Run(db, db, db, Options{Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st3.Written, "exactly the mutated row updates")
	assert.Equal(t, 3, st3.Unchanged)
	require.NoError(t, db.First(&m1, "galgame_id = ?", 101).Error)
	require.NotNil(t, m1.WishlistCount)
	assert.EqualValues(t, 301, *m1.WishlistCount, "refreshed value lands")
	assert.True(t, m1.SyncedAt.After(before), "synced_at advances on real change")
}

// assertCounts pins the funnel; secondPass flips the Written/Unchanged split
// (the plan is identical, only the change detection outcome differs).
func assertCounts(t *testing.T, s *Stats, secondPass bool) {
	t.Helper()
	// Anchors = exact+claimed+galgame-medium join rows: #1 #5 #6 #7 #9 + two for #8 = 7.
	assert.Equal(t, 7, s.Anchors, "probable/unclaimed/asmr excluded from anchors")
	assert.Equal(t, 1, s.MultiAnchor, "#8's second anchor collapsed")
	assert.Equal(t, 5, s.Matched, "#1 #5 #6 #8 #9 have a mirror row (#7 missing)")
	if secondPass {
		assert.Zero(t, s.Written, "unchanged mirror → zero effective writes")
		assert.Equal(t, 4, s.Unchanged)
	} else {
		assert.Equal(t, 4, s.Written, "#1 #5 #8 #9 (#6 galgame absent)")
		assert.Zero(t, s.Unchanged)
	}
	assert.Equal(t, 1, s.MissingInMirror, "#7 workno absent from mirror")
	assert.Equal(t, 1, s.SkippedNoGalgame, "#6 target galgame absent from wiki")
}
