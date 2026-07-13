package catalogsync

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds), the src_bangumi Silver schema, and the wiki
// galgame table all live in ONE database — plus a stand-in `games` table for
// the erogamespace staging source. wiki==catalog==eg is the same handle here,
// exactly as CI co-locates the schemas.
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
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(&gmodel.Galgame{}); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: galgame automigrate failed: %v\n", err)
		os.Exit(0)
	}
	// Stand-in for the erogamespace staging `games` table.
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS games (id bigint, vndb text)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: games table failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_external_ref", "catalog_revision", "catalog_redirect",
		"catalog_work_title", "catalog_release", "catalog_work",
		"galgame", "src_bangumi.subject", "games",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func seedSubject(t *testing.T, id int64, typ int, name, nameCN, date string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Subject{
		ID: id, Type: typ, Name: name, NameCN: nameCN, Date: date,
		InfoboxRaw: "", ParseError: "", Summary: "", ParserVersion: srcb.ParserVersion,
		IngestedAt: time.Now(),
	}).Error)
}

func seedGame(t *testing.T, id int64, vndbID string, bid *int, ja, zh string, year int) {
	t.Helper()
	var rd, bidVal any
	if year > 0 {
		rd = fmt.Sprintf("%d-01-01", year)
	}
	if bid != nil {
		bidVal = *bid
	}
	require.NoError(t, testDB.Exec(`
		INSERT INTO galgame (id, vndb_id, bid, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
		                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw,
		                     release_date, status, user_id)
		VALUES (?, ?, ?, '', ?, ?, '', '', '', '', '', ?, 0, 1)
	`, id, vndbID, bidVal, ja, zh, rd).Error)
}

func seedEG(t *testing.T, id int64, vndb string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO games (id, vndb) VALUES (?, ?)`, id, vndb).Error)
}

func TestReconcileScenarios(t *testing.T) {
	clean(t)

	// Subjects (type=4 games unless noted). 500 is game1's audit-ok bid; 600 is
	// a wrong-type (anime) bid; the rest are the title-match RHS.
	seedSubject(t, 500, 4, "ゲーム500", "游戏500", "2005-01-01")
	seedSubject(t, 600, 2, "アニメ600", "", "2006-01-01") // wrong-type
	seedSubject(t, 800, 4, "タイトルエックス", "", "2010-05-01")
	seedSubject(t, 810, 4, "タイトルワイ", "", "2009-01-01") // year 2009
	seedSubject(t, 820, 4, "タイトルゼット", "", "2012-01-01")
	seedSubject(t, 830, 4, "タイトルマルチ", "", "2013-01-01")
	seedSubject(t, 831, 4, "タイトルマルチ", "", "2013-01-01")
	seedSubject(t, 840, 4, "ジェーピー840", "中文标题", "2014-01-01")

	bid500, bid600, bid700 := 500, 600, 700
	// game1: audit-ok bid + vndb.
	seedGame(t, 1, "v100", &bid500, "ゲーム500", "游戏500", 2005)
	// game2: wrong-type bid; title shared with game5.
	seedGame(t, 2, "v200", &bid600, "タイトルゼット", "", 2012)
	// game3: missing bid; title year mismatch (2011 vs subject 810's 2009).
	seedGame(t, 3, "v300", &bid700, "タイトルワイ", "", 2011)
	// game4: no anchor; unique title match.
	seedGame(t, 4, "", nil, "タイトルエックス", "", 2010)
	// game5: shares subject 820 with game2 (one-to-many, subject side).
	seedGame(t, 5, "", nil, "タイトルゼット", "", 2012)
	// game6: matches subjects 830 AND 831 (one-to-many, game side).
	seedGame(t, 6, "", nil, "タイトルマルチ", "", 2013)
	// game7: zh-side title match.
	seedGame(t, 7, "", nil, "", "中文标题", 2014)

	seedEG(t, 1000, "v100") // unique → game1
	seedEG(t, 1001, "v200") // ambiguous pair (game2 skipped)
	seedEG(t, 1002, "v200")
	seedEG(t, 1003, "v999") // no wiki game — silent absence

	ctx := context.Background()

	// --- dry run: reports the full plan, writes nothing ---
	ds, err := New(testDB, testDB, testDB, Options{DryRun: true}).Run(ctx, PhaseAll)
	require.NoError(t, err)
	assert.Equal(t, 7, ds.Claim.ClaimedNew)
	assert.Equal(t, 3, ds.Claim.VNDBExact)
	assert.Equal(t, 1, ds.Claim.BIDExact)
	assert.Equal(t, 2, ds.Claim.BIDSkippedBad)
	assert.Equal(t, 1, ds.EG.Matched)
	assert.Equal(t, 1, ds.EG.AmbiguousSkipped)
	assert.Equal(t, 1, ds.EG.RefsWritten)
	assert.Equal(t, 5, ds.Bangumi.CandidatesLHS)
	assert.Equal(t, 2, ds.Bangumi.MatchedUnique)
	assert.Equal(t, 3, ds.Bangumi.RejectedAmbiguous)
	assert.Equal(t, 2, ds.Bangumi.RefsWritten)
	assert.Zero(t, count(t, "catalog_work"), "dry run must not write")

	// --- apply run 1 ---
	rc := New(testDB, testDB, testDB, Options{})
	s1, err := rc.Run(ctx, PhaseAll)
	require.NoError(t, err)
	assert.Equal(t, 7, s1.Claim.ClaimedNew)
	assert.Equal(t, 1, s1.EG.RefsWritten)
	assert.Equal(t, 2, s1.Bangumi.MatchedUnique)
	assert.Equal(t, 3, s1.Bangumi.RejectedAmbiguous)
	assert.Equal(t, 2, s1.Bangumi.RefsWritten)

	assert.Equal(t, int64(7), count(t, "catalog_work"))
	assert.Equal(t, int64(7), count(t, "catalog_external_ref"))
	assert.Equal(t, int64(7), count(t, "catalog_revision"), "one created revision per claimed work")

	// Regression (step-11 REWORK): work.site is the ECOSYSTEM TENANT KEY
	// "galgame_wiki", NOT the medium key "galgame" — consumers scope by tenant.
	var site string
	require.NoError(t, testDB.Raw(`SELECT site FROM catalog_work WHERE product_work_id=1`).Scan(&site).Error)
	assert.Equal(t, "galgame_wiki", site, "work.site must be the tenant key galgame_wiki, not the medium key galgame")

	// game1: vndb exact + bid exact + eg probable.
	w1 := refsOf(t, workOf(t, 1))
	assert.Len(t, w1, 3)
	assert.True(t, hasRef(w1, sourceVNDB, "v100", model.LinkKindExact, ruleWikiVNDB))
	assert.True(t, hasRef(w1, sourceBangumi, "500", model.LinkKindExact, ruleWikiBID))
	assert.True(t, hasRef(w1, sourceEG, "1000", model.LinkKindProbable, ruleEGRosetta))

	// game2: only its vndb exact (bad bid → no anchor; EG ambiguous; title lost).
	w2 := refsOf(t, workOf(t, 2))
	assert.Len(t, w2, 1)
	assert.True(t, hasRef(w2, sourceVNDB, "v200", model.LinkKindExact, ruleWikiVNDB))

	// game3: only vndb exact — the year gate blocked its title-equal subject.
	w3 := refsOf(t, workOf(t, 3))
	assert.Len(t, w3, 1)
	assert.True(t, hasRef(w3, sourceVNDB, "v300", model.LinkKindExact, ruleWikiVNDB))

	// game4: unique title match → probable bangumi ref.
	w4 := refsOf(t, workOf(t, 4))
	assert.Len(t, w4, 1)
	assert.True(t, hasRef(w4, sourceBangumi, "800", model.LinkKindProbable, ruleTitleYear))

	// game5 & game6: one-to-many ambiguity → nothing written.
	assert.Empty(t, refsOf(t, workOf(t, 5)))
	assert.Empty(t, refsOf(t, workOf(t, 6)))

	// game7: zh-side unique title match → probable bangumi ref.
	w7 := refsOf(t, workOf(t, 7))
	assert.Len(t, w7, 1)
	assert.True(t, hasRef(w7, sourceBangumi, "840", model.LinkKindProbable, ruleTitleYear))

	// --- ref never re-graded + second-pass zero write ---
	// A human promotes game4's title ref to exact; a re-run must NOT touch it.
	require.NoError(t, testDB.Exec(`UPDATE catalog_external_ref SET link_kind=?, matched_by='human:9'
		WHERE entity_type=? AND entity_id=? AND source_id=? AND external_id='800'`,
		model.LinkKindExact, model.EntityTypeWork, workOf(t, 4), sourceBangumi).Error)

	s2, err := rc.Run(ctx, PhaseAll)
	require.NoError(t, err)
	assert.Zero(t, s2.Claim.ClaimedNew)
	assert.Equal(t, 7, s2.Claim.AlreadyClaimed)
	assert.Zero(t, s2.EG.RefsWritten)
	assert.Equal(t, 1, s2.EG.Already)
	assert.Zero(t, s2.Bangumi.RefsWritten)
	assert.Equal(t, 2, s2.Bangumi.Already)

	var promoted model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_type=? AND entity_id=? AND source_id=? AND external_id='800'",
		model.EntityTypeWork, workOf(t, 4), sourceBangumi).First(&promoted).Error)
	assert.Equal(t, model.LinkKindExact, promoted.LinkKind, "import must never re-grade an existing ref")
	assert.Equal(t, "human:9", promoted.MatchedBy)

	assert.Equal(t, int64(7), count(t, "catalog_work"))
	assert.Equal(t, int64(7), count(t, "catalog_external_ref"), "second pass writes nothing")
	assert.Equal(t, int64(7), count(t, "catalog_revision"), "second pass mints no revision")
}

// TestReconcileWritesBackCatalogWorkID pins the step-34 T1 cross-face pointer:
// claiming a work backfills wiki galgame.catalog_work_id, idempotently, and a
// re-run repairs a NULLed / stale cell (the up-front already-claimed backfill).
func TestReconcileWritesBackCatalogWorkID(t *testing.T) {
	clean(t)
	seedGame(t, 1, "v100", nil, "ゲーム1", "", 2020)
	seedGame(t, 2, "v200", nil, "ゲーム2", "", 2021)
	ctx := context.Background()

	catalogWorkID := func(gid int64) *int64 {
		t.Helper()
		var cwid *int64
		require.NoError(t, testDB.Raw(`SELECT catalog_work_id FROM galgame WHERE id=?`, gid).Scan(&cwid).Error)
		return cwid
	}

	// apply run 1: both works claimed → both pointers backfilled.
	rc := New(testDB, testDB, testDB, Options{})
	s1, err := rc.Run(ctx, PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 2, s1.Claim.ClaimedNew)
	assert.Equal(t, 2, s1.Claim.WorkIDBackfilled, "both new claims backfilled")
	for _, gid := range []int64{1, 2} {
		got := catalogWorkID(gid)
		require.NotNil(t, got, "galgame %d catalog_work_id set", gid)
		assert.Equal(t, workOf(t, gid), *got, "galgame %d points at its work", gid)
	}

	// apply run 2: converged — the backfill writes nothing.
	s2, err := rc.Run(ctx, PhaseClaim)
	require.NoError(t, err)
	assert.Zero(t, s2.Claim.WorkIDBackfilled, "second pass backfills nothing")
	assert.Equal(t, 2, s2.Claim.WorkIDCovered, "both already-claimed works covered")

	// robustness: NULL one cell → the up-front pass repairs exactly it, even
	// though no new claim happens (the pointer's only writer is reconcile).
	require.NoError(t, testDB.Exec(`UPDATE galgame SET catalog_work_id=NULL WHERE id=1`).Error)
	s3, err := rc.Run(ctx, PhaseClaim)
	require.NoError(t, err)
	assert.Equal(t, 1, s3.Claim.WorkIDBackfilled, "only the reset cell is repaired")
	got := catalogWorkID(1)
	require.NotNil(t, got)
	assert.Equal(t, workOf(t, 1), *got)
}

// --- helpers ---------------------------------------------------------------

func count(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Table(table).Count(&n).Error)
	return n
}

func workOf(t *testing.T, galgameID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(
		`SELECT id FROM catalog_work WHERE site=? AND product_work_id=?`, siteGalgame, galgameID,
	).Scan(&id).Error)
	require.NotZero(t, id, "work for galgame %d", galgameID)
	return id
}

func refsOf(t *testing.T, workID int64) []model.CatalogExternalRef {
	t.Helper()
	var refs []model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_type=? AND entity_id=?", model.EntityTypeWork, workID).Find(&refs).Error)
	return refs
}

func hasRef(refs []model.CatalogExternalRef, source int16, ext string, kind int16, matchedBy string) bool {
	for _, r := range refs {
		if r.SourceID == source && r.ExternalID == ext && r.LinkKind == kind && r.MatchedBy == matchedBy {
			return true
		}
	}
	return false
}
