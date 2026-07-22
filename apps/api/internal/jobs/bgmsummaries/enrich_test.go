package bgmsummaries

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
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
// in ONE database, exactly as production lays them out (src_bangumi is a
// schema inside kun_catalog — the single-DSN premise of this job). Run drives
// the DSN itself, so we capture it (not just the handle) to exercise the real
// entry point.
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
		"catalog_work_intro", "catalog_external_ref", "catalog_work", "src_bangumi.subject",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkWork(t *testing.T, medium int16, name string, site *string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name, Site: site}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func mkAnchor(t *testing.T, workID, subjectID int64, source int16, kind int16, matchedBy string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: source,
		ExternalID: strconv.FormatInt(subjectID, 10), LinkKind: kind, MatchedBy: matchedBy,
	}).Error)
}

func mkSubject(t *testing.T, id int64, summary string) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcb.Subject{
		ID: id, Type: 4, Name: fmt.Sprintf("subject-%d", id), NameCN: "",
		InfoboxRaw: "", ParseError: "", Summary: summary, Date: "",
		ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}).Error)
}

func mkIntro(t *testing.T, workID int64, lang, intro string, source int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkIntro{
		WorkID: workID, Lang: lang, Intro: intro, SourceID: source,
	}).Error)
}

func introCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_intro "+where, args...).Scan(&n).Error)
	return n
}

// TestFillMissingLanguage exercises the whole pipeline through the real Run
// entry point: candidate selection (exact anchors only, bodyless only),
// fill-missing-language decisions, CRLF normalization, dry-run zero-write,
// apply, and second-pass idempotency.
func TestFillMissingLanguage(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)
	var dlsite int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&dlsite).Error)
	require.NotZero(t, dlsite)

	claimed := "galgame_wiki"
	wZh := mkWork(t, reg.galgameMedium, "has-ja-gets-zh", nil)        // ja intro exists, zh summary → writes zh-Hans
	wJaDup := mkWork(t, reg.galgameMedium, "has-ja-dup-ja", nil)      // ja intro exists, ja summary → skipped
	wJaFill := mkWork(t, reg.galgameMedium, "no-intro-gets-ja", nil)  // no intro, ja summary (CRLF) → writes ja
	wEmpty := mkWork(t, reg.galgameMedium, "blank-summary", nil)      // whitespace-only summary → no_summary
	wClaimed := mkWork(t, reg.galgameMedium, "claimed", &claimed)     // claimed → excluded by SQL
	wProbable := mkWork(t, reg.galgameMedium, "probable-anchor", nil) // probable tier → excluded by SQL

	zhSummary := "两位新人冒险者发现自己被困孤岛。"
	jaDupSummary := "ふたりの冒険者が孤島に流れ着く。"
	jaCRLFSummary := "一行目です。\r\n二行目です。"
	mkSubject(t, 1001, zhSummary)
	mkSubject(t, 1002, jaDupSummary)
	mkSubject(t, 1003, jaCRLFSummary)
	mkSubject(t, 1004, " \r\n ")
	mkSubject(t, 1005, "claimed のあらすじ")
	mkSubject(t, 1006, "probable のあらすじ")

	mkAnchor(t, wZh, 1001, reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wJaDup, 1002, reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wJaFill, 1003, reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wEmpty, 1004, reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wClaimed, 1005, reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wProbable, 1006, reg.bangumiSource, model.LinkKindProbable, "rule:bgm-title-only")

	mkIntro(t, wZh, "ja", "DLsite の紹介文。", dlsite)
	mkIntro(t, wJaDup, "ja", "既にある日本語紹介。", dlsite)

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 4, st.Candidates, "claimed + probable are excluded in SQL")
	assert.Equal(t, 1, st.ZhNew, "wZh: zh summary, no zh intro yet")
	assert.Equal(t, 1, st.JaFill, "wJaFill: ja summary, no intro at all")
	assert.Equal(t, 1, st.SkipDupLang, "wJaDup: ja summary duplicates the DLsite ja intro")
	assert.Equal(t, 1, st.NoSummary)
	assert.Equal(t, 0, st.Refused, "no claimed work reaches the write path")
	assert.Zero(t, st.ZhWritten+st.JaWritten+st.Conflict+st.Errors)
	assert.EqualValues(t, 2, introCount(t, ""), "dry run writes nothing (the two fixtures only)")
	require.Len(t, st.ZhSamples, 1)
	assert.Equal(t, wZh, st.ZhSamples[0].WorkID)
	assert.Equal(t, "zh-Hans", st.ZhSamples[0].Lang)

	// --- apply: writes exactly wZh(zh-Hans) + wJaFill(ja).
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.ZhWritten)
	assert.Equal(t, 1, st.JaWritten)
	assert.Equal(t, 0, st.Conflict)
	assert.Equal(t, 0, st.Errors)

	// Fresh dest per query — a reused struct's populated primary key would be
	// folded into the next First's conditions (GORM behavior).
	var rowZh, rowJa model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wZh, reg.bangumiSource).First(&rowZh).Error)
	assert.Equal(t, "zh-Hans", rowZh.Lang)
	assert.Equal(t, zhSummary, rowZh.Intro, "text verbatim")
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wJaFill, reg.bangumiSource).First(&rowJa).Error)
	assert.Equal(t, "ja", rowJa.Lang)
	assert.Equal(t, "一行目です。\n二行目です。", rowJa.Intro, "CRLF normalized to LF")
	assert.EqualValues(t, 0, introCount(t, "WHERE work_id = ? AND source_id = ?", wJaDup, reg.bangumiSource),
		"duplicate-language summary never lands")
	assert.EqualValues(t, 0, introCount(t, "WHERE work_id = ?", wClaimed), "claimed work never materialised")
	assert.EqualValues(t, 4, introCount(t, ""), "2 fixtures + 2 writes")

	// --- second apply: fill-missing is idempotent — zero writes, the two
	// written langs now skip as duplicates.
	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.ZhWritten+st.JaWritten+st.Errors, "second pass writes zero")
	assert.Equal(t, 3, st.SkipDupLang, "wZh + wJaFill now have their langs; wJaDup still dup")
	assert.EqualValues(t, 4, introCount(t, ""), "row count unchanged")
}

// TestNonTitleYearExactAnchorEntersCandidates pins the step-79 fix: an EXACT
// Bangumi work anchor whose matched_by is NOT rule:bgm-title-year (here the
// wave-78 rule:bgm-type4-gated tier) now enters the candidate set. Before the
// fix the hard-coded matched_by filter left the 11,465 new anchors invisible
// (candidates stuck at ~1,875). The exact gate is unchanged: a probable anchor
// still stays out.
func TestNonTitleYearExactAnchorEntersCandidates(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	wGated := mkWork(t, reg.galgameMedium, "gated-anchor", nil)
	mkSubject(t, 3001, "ゲートされたあらすじ。")
	mkAnchor(t, wGated, 3001, reg.bangumiSource, model.LinkKindExact, "rule:bgm-type4-gated")

	// A different exact tier (the xmedia importer's rule) is also admitted.
	wXmedia := mkWork(t, reg.galgameMedium, "xmedia-anchor", nil)
	mkSubject(t, 3002, "クロスメディアのあらすじ。")
	mkAnchor(t, wXmedia, 3002, reg.bangumiSource, model.LinkKindExact, "rule:bangumi-xmedia-import")

	// A probable anchor stays excluded — only matched_by was loosened, the exact
	// gate is intact.
	wProbable := mkWork(t, reg.galgameMedium, "probable-anchor", nil)
	mkSubject(t, 3003, "確度の低いあらすじ。")
	mkAnchor(t, wProbable, 3003, reg.bangumiSource, model.LinkKindProbable, "rule:bgm-title-only")

	st, err := Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Candidates, "both non-title-year exact anchors are candidates; probable stays out")
	assert.EqualValues(t, 1, introCount(t, "WHERE work_id = ?", wGated), "gated-rule work gets its intro")
	assert.EqualValues(t, 1, introCount(t, "WHERE work_id = ?", wXmedia), "xmedia-rule work gets its intro")
	assert.EqualValues(t, 0, introCount(t, "WHERE work_id = ?", wProbable), "probable work stays out")
}

// TestXORAndConflictBackstop drives the write path directly (the SQL filter
// excludes claimed works from candidates, so the guard is only reachable
// here): a claimed candidate is refused, and a STALE exist map still cannot
// duplicate a row thanks to the (work_id,lang,source_id) ON CONFLICT.
func TestXORAndConflictBackstop(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"
	wClaimed := mkWork(t, reg.galgameMedium, "claimed-direct", &claimed)
	wBody := mkWork(t, reg.galgameMedium, "bodyless-direct", nil)

	// XOR: a claimed candidate is refused before any language decision.
	r := &runner{db: testDB, sourceID: reg.bangumiSource, exist: map[int64]map[string]bool{}, stats: &Stats{}}
	r.enrich(ctx, candidate{WorkID: wClaimed, SubjectID: 2001, Site: &claimed, Summary: "あらすじ"}, true)
	assert.Equal(t, 1, r.stats.Refused)
	assert.Zero(t, r.stats.JaFill+r.stats.JaWritten)
	assert.EqualValues(t, 0, introCount(t, ""))

	// Backstop: write once, then retry with an EMPTY exist map — the primary
	// fill-missing skip is blind, but the unique key refuses the duplicate.
	r.enrich(ctx, candidate{WorkID: wBody, SubjectID: 2002, Site: nil, Summary: "あらすじ"}, true)
	assert.Equal(t, 1, r.stats.JaWritten)
	rStale := &runner{db: testDB, sourceID: reg.bangumiSource, exist: map[int64]map[string]bool{}, stats: &Stats{}}
	rStale.enrich(ctx, candidate{WorkID: wBody, SubjectID: 2002, Site: nil, Summary: "あらすじ"}, true)
	assert.Equal(t, 1, rStale.stats.Conflict, "ON CONFLICT refuses the duplicate")
	assert.Equal(t, 0, rStale.stats.JaWritten)
	assert.EqualValues(t, 1, introCount(t, ""), "still exactly one row")
}
