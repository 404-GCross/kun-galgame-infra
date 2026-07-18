package doujinbangumi

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds) and the src_bangumi Silver schema co-located
// in ONE database, exactly as CI lays them out. Run drives the DSN itself, so
// we capture it (not just the handle) to exercise the real entry point.
var (
	testDB  *gorm.DB
	testDSN string
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
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_external_ref", "catalog_revision", "catalog_release",
		"catalog_work_title", "catalog_work", "src_bangumi.subject",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkWork(t *testing.T, medium int16, name string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name} // Site nil = bodyless
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkTitle(t *testing.T, workID int64, lang, title string, kind int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkTitle{WorkID: workID, Lang: lang, Title: title, Kind: kind}).Error)
}

func mkRelease(t *testing.T, workID int64, year int16) {
	t.Helper()
	r := model.CatalogRelease{WorkID: workID, Kind: model.ReleaseKindDefault}
	if year > 0 {
		r.ReleasedY = &year
	}
	require.NoError(t, testDB.Create(&r).Error)
}

func seedSubject(t *testing.T, id int64, typ int, name, nameCN, date string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Subject{
		ID: id, Type: typ, Name: name, NameCN: nameCN, Date: date,
		InfoboxRaw: "", ParseError: "", Summary: "", ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}).Error)
}

func refsOf(t *testing.T, workID int64) []model.CatalogExternalRef {
	t.Helper()
	var refs []model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_type = ? AND entity_id = ?", model.EntityTypeWork, workID).Find(&refs).Error)
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

func countRefs(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Table("catalog_external_ref").Count(&n).Error)
	return n
}

// TestReconcileScenarios pins every tier + guard on one fixture: exact (title +
// corroborating year, ja and cn sides), probable (no year / year disagrees),
// the work-side and bangumi-side (squatting) ambiguity skips, the length floor,
// the type=4 filter, the already-anchored idempotency skip, never-re-grade, and
// the second-pass-zero-write guarantee — all driven through the real Run entry
// point (DSN → open → resolve → decide → write).
func TestReconcileScenarios(t *testing.T) {
	clean(t)
	reg, err := resolveRegistry(context.Background(), testDB)
	require.NoError(t, err)
	require.EqualValues(t, 3, reg.bangumiSource, "bangumi source id must resolve to 3")

	galgame := reg.galgameMedium
	other := int16(2) // manga — a non-galgame medium, must never be a candidate

	// --- subjects (type=4 games unless noted) ---
	seedSubject(t, 5001, 4, "ぜっとゲームアルファexact", "", "2011-05-01")     // W1: year diff 1 → exact boundary
	seedSubject(t, 5002, 4, "ぷろばぶるゲームベータ", "", "2011")               // W2: work has no year → probable
	seedSubject(t, 5003, 4, "みすまっちゲームガンマ", "", "2015-01-01")         // W3: year disagree → probable
	seedSubject(t, 5004, 4, "JPTITLE5004", "中文标题作品cn", "2012-03-01") // W4: cn-side match → exact
	seedSubject(t, 5005, 4, "あいまいさくひんファイブ", "", "2013-01-01")        // W5: shares title with 5055
	seedSubject(t, 5055, 4, "あいまいさくひんファイブ", "", "2013-01-01")        // → W5 hits two subjects (work-side ambig)
	seedSubject(t, 5006, 4, "きょうゆうさぶじぇくとシックス", "", "2014-01-01")     // W6+W7 both hit it (squatting)
	seedSubject(t, 5009, 4, "あんかーどナインexact", "", "2010-01-01")       // W9: already-anchored, never re-graded
	seedSubject(t, 5100, 2, "あにめだけのやつゼロ", "", "2016-01-01")          // type=2 anime — must be ignored
	seedSubject(t, 5200, 4, "みじか", "", "2017-01-01")                 // 3 chars — below the length floor

	// --- works (all bodyless galgame unless noted) ---
	w1 := mkWork(t, galgame, "W1")
	mkTitle(t, w1, "ja", "ぜっとゲームアルファexact", model.WorkTitleKindOfficial)
	mkRelease(t, w1, 2010) // |2010-2011| = 1 → exact

	w2 := mkWork(t, galgame, "W2")
	mkTitle(t, w2, "ja", "ぷろばぶるゲームベータ", model.WorkTitleKindOfficial) // no release row → no year

	w3 := mkWork(t, galgame, "W3")
	mkTitle(t, w3, "ja", "みすまっちゲームガンマ", model.WorkTitleKindOfficial)
	mkRelease(t, w3, 2000) // |2000-2015| = 15 → probable

	w4 := mkWork(t, galgame, "W4")
	mkTitle(t, w4, "zh-Hans", "中文标题作品cn", model.WorkTitleKindOfficial)
	mkRelease(t, w4, 2012) // cn match, |2012-2012| = 0 → exact

	w5 := mkWork(t, galgame, "W5")
	mkTitle(t, w5, "ja", "あいまいさくひんファイブ", model.WorkTitleKindOfficial)
	mkRelease(t, w5, 2013) // hits 5005 AND 5055 → work-side ambiguous

	w6 := mkWork(t, galgame, "W6")
	mkTitle(t, w6, "ja", "きょうゆうさぶじぇくとシックス", model.WorkTitleKindOfficial)
	mkRelease(t, w6, 2014)
	w7 := mkWork(t, galgame, "W7")
	mkTitle(t, w7, "ja", "きょうゆうさぶじぇくとシックス", model.WorkTitleKindOfficial)
	mkRelease(t, w7, 2014) // W6+W7 both hit 5006 → bangumi-side ambiguous (squatting guard)

	w8 := mkWork(t, galgame, "W8")
	mkTitle(t, w8, "ja", "みじか", model.WorkTitleKindOfficial) // 3 chars → not a candidate at all

	w9 := mkWork(t, galgame, "W9")
	mkTitle(t, w9, "ja", "あんかーどナインexact", model.WorkTitleKindOfficial)
	mkRelease(t, w9, 2010) // would be exact, but is already anchored (below)

	w10 := mkWork(t, galgame, "W10")
	mkTitle(t, w10, "ja", "あにめだけのやつゼロ", model.WorkTitleKindOfficial)
	mkRelease(t, w10, 2016) // only matches a type=2 subject → no match

	// A non-galgame work whose title equals an exact subject — the medium filter
	// must exclude it entirely (never a candidate, never anchored).
	wm := mkWork(t, other, "Wmanga")
	mkTitle(t, wm, "ja", "ぜっとゲームアルファexact", model.WorkTitleKindOfficial)
	mkRelease(t, wm, 2011)

	// Pre-existing human-confirmed EXACT anchor on W9 → idempotency skip + the
	// never-re-grade witness (a later probable/exact pass must not touch it).
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: w9, SourceID: reg.bangumiSource,
		ExternalID: "5009", LinkKind: model.LinkKindExact, MatchedBy: "human:9",
	}).Error)

	ctx := context.Background()

	// --- dry run: reports the plan, writes nothing ---
	dry, err := Run(ctx, Opts{DSN: testDSN, Apply: false})
	require.NoError(t, err)
	assert.Equal(t, 9, dry.CandidateWorks, "W1..W7, W9, W10 — W8 (short title) and the manga work excluded")
	assert.Equal(t, 1, dry.AlreadyAnchored, "W9")
	assert.Equal(t, 7, dry.Matched, "W1..W7 (W9 skipped before match, W10 no match)")
	assert.Equal(t, 2, dry.Exact, "W1 (year±1) + W4 (cn)")
	assert.Equal(t, 2, dry.Probable, "W2 (no year) + W3 (year disagree)")
	assert.Equal(t, 1, dry.AmbiguousWork, "W5")
	assert.Equal(t, 2, dry.AmbiguousSubject, "W6 + W7 squatting")
	assert.Zero(t, dry.ExactWritten+dry.ProbableWritten, "dry writes nothing")
	assert.EqualValues(t, 1, countRefs(t), "dry leaves only the injected W9 anchor")
	assert.Len(t, dry.ExactSamples, 2)
	assert.Len(t, dry.ProbableSamples, 2)

	// --- apply run 1 ---
	a1, err := Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 2, a1.ExactWritten)
	assert.Equal(t, 2, a1.ProbableWritten)
	assert.Zero(t, a1.Already)
	assert.EqualValues(t, 5, countRefs(t), "4 new + the injected W9 anchor")

	assert.True(t, hasRef(refsOf(t, w1), reg.bangumiSource, "5001", model.LinkKindExact, ruleTitleYear), "W1 exact title-year")
	assert.True(t, hasRef(refsOf(t, w4), reg.bangumiSource, "5004", model.LinkKindExact, ruleTitleYear), "W4 exact via cn")
	assert.True(t, hasRef(refsOf(t, w2), reg.bangumiSource, "5002", model.LinkKindProbable, ruleTitleOnly), "W2 probable (no year)")
	assert.True(t, hasRef(refsOf(t, w3), reg.bangumiSource, "5003", model.LinkKindProbable, ruleTitleOnly), "W3 probable (year disagree)")

	// squatting guard + ambiguity + excluded works never got an anchor
	for _, w := range []int64{w5, w6, w7, w8, w10, wm} {
		assert.Empty(t, refsOf(t, w), "work %d must carry no anchor", w)
	}

	// --- never-re-grade: W9's human EXACT anchor is untouched ---
	w9refs := refsOf(t, w9)
	require.Len(t, w9refs, 1)
	assert.Equal(t, model.LinkKindExact, w9refs[0].LinkKind, "existing exact never demoted")
	assert.Equal(t, "human:9", w9refs[0].MatchedBy)

	// --- apply run 2: idempotent, writes zero ---
	a2, err := Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, a2.ExactWritten+a2.ProbableWritten, "second apply writes nothing")
	assert.Equal(t, 5, a2.AlreadyAnchored, "W1..W4 now anchored + W9")
	assert.Zero(t, a2.Exact)
	assert.Zero(t, a2.Probable)
	assert.EqualValues(t, 5, countRefs(t), "second pass mints no ref")
}

// TestDSNRequired pins the never-defaulted-DSN discipline: a bare run refuses
// rather than guessing at a database.
func TestDSNRequired(t *testing.T) {
	_, err := Run(context.Background(), Opts{DSN: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--dsn")
}

// TestInsertRefNeverReGrade witnesses the write primitive directly: once an
// exact assertion exists, re-proposing the SAME (entity, source, external_id)
// at the probable tier is a no-op — InsertRefIfAbsent never re-grades. This is
// the backstop under the tool's per-work idempotency skip.
func TestInsertRefNeverReGrade(t *testing.T) {
	clean(t)
	reg, err := resolveRegistry(context.Background(), testDB)
	require.NoError(t, err)
	workID := mkWork(t, reg.galgameMedium, "primitive")
	ext := strconv.FormatInt(7777, 10)

	first, err := repository.InsertRefIfAbsent(testDB, model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: reg.bangumiSource,
		ExternalID: ext, LinkKind: model.LinkKindExact, MatchedBy: ruleTitleYear,
	})
	require.NoError(t, err)
	assert.True(t, first, "first insert writes")

	second, err := repository.InsertRefIfAbsent(testDB, model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: reg.bangumiSource,
		ExternalID: ext, LinkKind: model.LinkKindProbable, MatchedBy: ruleTitleOnly,
	})
	require.NoError(t, err)
	assert.False(t, second, "second insert at a different tier is a no-op")

	refs := refsOf(t, workID)
	require.Len(t, refs, 1)
	assert.Equal(t, model.LinkKindExact, refs[0].LinkKind, "tier unchanged")
	assert.Equal(t, ruleTitleYear, refs[0].MatchedBy, "matched_by unchanged")
}
