package workratings

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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + registry seeds), the src_bangumi Silver schema, and a minimal
// EG mirror `games` table ALL co-located in ONE database. The EG fixture lives
// in its OWN schema (workratings_eg) reached via a search_path DSN — the
// shared test DB's public.games belongs to importer_test.go (a different
// shape) and must not be clobbered. Run drives the DSNs itself, so we capture
// them (not just the handle) to exercise the real entry point.
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
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	// Minimal EG mirror shape (only the columns loadEGMirror touches), in a
	// DEDICATED schema so the shared test DB's public.games (importer_test.go's
	// fixture, a different shape) is never clobbered. egTestDSN resolves
	// unqualified `games` there via search_path.
	if err := db.Exec(`CREATE SCHEMA IF NOT EXISTS workratings_eg`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: eg fixture schema failed: %v\n", err)
		os.Exit(0)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS workratings_eg.games (id int PRIMARY KEY, median int, count2 int)`).Error; err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: games fixture failed: %v\n", err)
		os.Exit(0)
	}
	egTestDSN = testDSN + " options='-csearch_path=workratings_eg'"
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_work_rating", "catalog_external_ref", "catalog_work", "src_bangumi.subject", "workratings_eg.games",
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

func mkAnchor(t *testing.T, workID int64, externalID string, source, kind int16, matchedBy string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: matchedBy,
	}).Error)
}

func mkSubject(t *testing.T, id int64, score float64, rank int, details string) {
	t.Helper()
	sub := srcb.Subject{
		ID: id, Type: 4, Name: fmt.Sprintf("subject-%d", id), NameCN: "",
		InfoboxRaw: "", ParseError: "", Summary: "", Date: "",
		Score: score, Rank: rank,
		ParserVersion: srcb.ParserVersion, IngestedAt: time.Now(),
	}
	if details != "" {
		sub.ScoreDetails = []byte(details)
	}
	require.NoError(t, testDB.Create(&sub).Error)
}

func mkEGGame(t *testing.T, id int, median *int, count2 int) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO workratings_eg.games (id, median, count2) VALUES (?, ?, ?)`, id, median, count2).Error)
}

func ratingCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_rating "+where, args...).Scan(&n).Error)
	return n
}

func p(v int) *int { return &v }

// TestBackfillWorkRatings exercises the whole pipeline through the real Run
// entry point: per-lane candidate selection (exact anchors only, bodyless
// only), the score>0 / NULL-median filters, vote_count derivation from
// score_details, rank NULL semantics, EG multi-anchor collapse, dry-run
// zero-write, apply value fidelity, and second-pass idempotency.
func TestBackfillWorkRatings(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"

	// --- bangumi lane fixtures ---
	wBgm := mkWork(t, reg.galgameMedium, "bgm-scored", nil)      // scored subject → planned (rank 321)
	wBgmZero := mkWork(t, reg.galgameMedium, "bgm-unrated", nil) // score 0 → bgm_no_score
	wBgmNoRank := mkWork(t, reg.galgameMedium, "bgm-norank", nil)
	wBgmClaimed := mkWork(t, reg.galgameMedium, "bgm-claimed", &claimed) // claimed → excluded by SQL
	wBgmProbable := mkWork(t, reg.galgameMedium, "bgm-probable", nil)    // probable tier → excluded by SQL
	mkSubject(t, 101, 7.4, 321, `{"1":0,"5":10,"7":20,"10":12}`)         // votes = 42
	mkSubject(t, 102, 0, 0, `{"1":0}`)                                   // unrated
	mkSubject(t, 103, 5.5, 0, `{"5":3}`)                                 // rank 0 → NULL rank, votes 3
	mkSubject(t, 104, 8.0, 1, `{"10":5}`)                                // behind a claimed work
	mkSubject(t, 105, 8.0, 1, `{"10":5}`)                                // behind a probable anchor
	mkAnchor(t, wBgm, "101", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wBgmZero, "102", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wBgmNoRank, "103", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wBgmClaimed, "104", reg.bangumiSource, model.LinkKindExact, ruleTitleYear)
	mkAnchor(t, wBgmProbable, "105", reg.bangumiSource, model.LinkKindProbable, "rule:bgm-title-only")

	// --- erogamespace lane fixtures ---
	wEg := mkWork(t, reg.galgameMedium, "eg-scored", nil)              // median 78 → planned
	wEgNoMedian := mkWork(t, reg.galgameMedium, "eg-nomedian", nil)    // NULL median → eg_no_median
	wEgMissing := mkWork(t, reg.galgameMedium, "eg-missing", nil)      // absent from mirror
	wEgMulti := mkWork(t, reg.galgameMedium, "eg-multianchor", nil)    // two anchors → most-voted wins
	wEgClaimed := mkWork(t, reg.galgameMedium, "eg-claimed", &claimed) // claimed → excluded by SQL
	mkEGGame(t, 1001, p(78), 40)
	mkEGGame(t, 1002, nil, 5)
	mkEGGame(t, 1004, p(50), 10) // fewer votes
	mkEGGame(t, 1014, p(90), 99) // most-voted → chosen
	mkEGGame(t, 1005, p(60), 20) // behind a claimed work
	mkAnchor(t, wEg, "1001", reg.egSource, model.LinkKindExact, "rule:test")
	mkAnchor(t, wEgNoMedian, "1002", reg.egSource, model.LinkKindExact, "rule:test")
	mkAnchor(t, wEgMissing, "1003", reg.egSource, model.LinkKindExact, "rule:test")
	mkAnchor(t, wEgMulti, "1004", reg.egSource, model.LinkKindExact, "rule:test")
	mkAnchor(t, wEgMulti, "1014", reg.egSource, model.LinkKindExact, "rule:test")
	mkAnchor(t, wEgClaimed, "1005", reg.egSource, model.LinkKindExact, "rule:test")

	// wBgm ALSO carries an EG anchor: both lanes hit one work → two rows, one
	// per source (the UNIQUE (work_id, source_id) lets sources coexist).
	mkEGGame(t, 1006, p(70), 7)
	mkAnchor(t, wBgm, "1006", reg.egSource, model.LinkKindExact, "rule:test")

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, Opts{DSN: testDSN, EGDSN: egTestDSN})
	require.NoError(t, err)
	assert.Equal(t, 3, st.BgmCandidates, "claimed + probable excluded in SQL")
	assert.Equal(t, 1, st.BgmNoScore)
	assert.Equal(t, 2, st.BgmPlanned, "wBgm + wBgmNoRank")
	assert.Equal(t, 5, st.EgCandidates, "wBgm's EG anchor joins the lane; claimed excluded")
	assert.Equal(t, 1, st.EgMultiAnchor, "wEgMulti's second anchor collapsed")
	assert.Equal(t, 1, st.EgMissingMirror)
	assert.Equal(t, 1, st.EgNoMedian)
	assert.Equal(t, 3, st.EgPlanned, "wEg + wEgMulti + wBgm")
	assert.Equal(t, 0, st.Refused, "no claimed work reaches the write path")
	assert.Zero(t, st.BgmWritten+st.EgWritten+st.BgmConflict+st.EgConflict+st.Errors)
	assert.EqualValues(t, 0, ratingCount(t, ""), "dry run writes nothing")
	require.NotEmpty(t, st.BgmSamples)
	assert.Equal(t, wBgm, st.BgmSamples[0].WorkID)
	assert.Equal(t, 42, st.BgmSamples[0].VoteCount, "vote_count = summed score_details buckets")

	// --- apply: writes the decided plan exactly.
	st, err = Run(ctx, Opts{DSN: testDSN, EGDSN: egTestDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 2, st.BgmWritten)
	assert.Equal(t, 3, st.EgWritten)
	assert.Zero(t, st.BgmConflict+st.EgConflict+st.Errors)
	assert.EqualValues(t, 5, ratingCount(t, ""))

	// Value fidelity: the bangumi row (native 0-10, rank, derived votes).
	var rBgm model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wBgm, reg.bangumiSource).First(&rBgm).Error)
	assert.InDelta(t, 7.4, rBgm.Score, 1e-9)
	assert.Equal(t, 42, rBgm.VoteCount)
	require.NotNil(t, rBgm.Rank)
	assert.Equal(t, 321, *rBgm.Rank)

	// Bangumi rank 0 → NULL rank.
	var rNoRank model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ?", wBgmNoRank).First(&rNoRank).Error)
	assert.Nil(t, rNoRank.Rank, "unranked subject stores NULL, never a fake 0")

	// The erogamespace row (native 0-100 median, rank always NULL).
	var rEg model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wEg, reg.egSource).First(&rEg).Error)
	assert.InDelta(t, 78, rEg.Score, 1e-9)
	assert.Equal(t, 40, rEg.VoteCount)
	assert.Nil(t, rEg.Rank)

	// Multi-anchor picked the most-voted EG game (1014).
	var rMulti model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ?", wEgMulti).First(&rMulti).Error)
	assert.InDelta(t, 90, rMulti.Score, 1e-9)
	assert.Equal(t, 99, rMulti.VoteCount)

	// Both lanes on one work → two rows, one per source.
	assert.EqualValues(t, 2, ratingCount(t, "WHERE work_id = ?", wBgm))
	assert.EqualValues(t, 0, ratingCount(t, "WHERE work_id IN (?, ?)", wBgmClaimed, wEgClaimed),
		"claimed works never materialise")

	// --- second apply: idempotent — zero writes, every planned row conflicts.
	st, err = Run(ctx, Opts{DSN: testDSN, EGDSN: egTestDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.BgmWritten+st.EgWritten+st.Errors, "second pass writes zero")
	assert.Equal(t, 2, st.BgmConflict)
	assert.Equal(t, 3, st.EgConflict)
	assert.EqualValues(t, 5, ratingCount(t, ""), "row count unchanged")
}

// TestXORGuardAndDSNRequired covers the write-time XOR guard (the SQL filter
// excludes claimed works from candidates, so the guard is only reachable by
// driving the writer directly) and the refuse-to-guess DSN discipline.
func TestXORGuardAndDSNRequired(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	claimed := "galgame_wiki"
	wClaimed := mkWork(t, reg.galgameMedium, "claimed-direct", &claimed)
	wBody := mkWork(t, reg.galgameMedium, "bodyless-direct", nil)

	// XOR: a claimed row is refused before any write.
	w := &writer{db: testDB, stats: &Stats{}}
	var written, conflict int
	w.write(ctx, plannedRow{WorkID: wClaimed, Site: &claimed, SourceID: reg.bangumiSource, Score: 7.0}, true, &written, &conflict)
	assert.Equal(t, 1, w.stats.Refused)
	assert.Zero(t, written+conflict)
	assert.EqualValues(t, 0, ratingCount(t, ""))

	// A bodyless row writes; a retry conflicts (the UNIQUE backstop) instead of
	// duplicating.
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.bangumiSource, Score: 7.0, VoteCount: 3}, true, &written, &conflict)
	assert.Equal(t, 1, written)
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.bangumiSource, Score: 7.0, VoteCount: 3}, true, &written, &conflict)
	assert.Equal(t, 1, conflict, "ON CONFLICT refuses the duplicate")
	assert.EqualValues(t, 1, ratingCount(t, ""), "still exactly one row")

	// DSN discipline: both DSNs are required, never guessed.
	_, err = Run(ctx, Opts{EGDSN: testDSN})
	require.Error(t, err)
	_, err = Run(ctx, Opts{DSN: testDSN})
	require.Error(t, err)
}
