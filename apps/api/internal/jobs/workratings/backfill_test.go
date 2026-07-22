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
// (migrate.Run + registry seeds), the src_bangumi Silver schema, and minimal
// EG + DLsite mirror fixtures ALL co-located in ONE database. Each mirror
// fixture lives in its OWN schema (workratings_eg / workratings_dl) reached
// via a search_path DSN — the shared test DB's public.games belongs to
// importer_test.go (a different shape) and must not be clobbered. Run drives
// the DSNs itself, so we capture them (not just the handle) to exercise the
// real entry point.
var (
	testDB    *gorm.DB
	testDSN   string
	egTestDSN string
	dlTestDSN string
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
	// Minimal mirror shapes (only the columns the loaders touch), each in a
	// DEDICATED schema so the shared test DB's public.games (importer_test.go's
	// fixture, a different shape) is never clobbered. The search_path DSNs
	// resolve the unqualified table names there.
	for _, ddl := range []string{
		`CREATE SCHEMA IF NOT EXISTS workratings_eg`,
		`CREATE TABLE IF NOT EXISTS workratings_eg.games (id int PRIMARY KEY, median int, count2 int)`,
		`CREATE SCHEMA IF NOT EXISTS workratings_dl`,
		`CREATE TABLE IF NOT EXISTS workratings_dl.works (workno text PRIMARY KEY, info_json jsonb)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: mirror fixture failed: %v\n", err)
			os.Exit(0)
		}
	}
	egTestDSN = testDSN + " options='-csearch_path=workratings_eg'"
	dlTestDSN = testDSN + " options='-csearch_path=workratings_dl'"
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_work_rating", "catalog_work_popularity", "catalog_external_ref", "catalog_release",
		"catalog_work", "src_bangumi.subject", "workratings_eg.games", "workratings_dl.works",
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

// mkReleaseAnchor attaches a RELEASE-level external ref (the DLsite anchor
// shape — worknos are SKU-natured and hang off catalog_release).
func mkReleaseAnchor(t *testing.T, workID int64, externalID string, source, kind int16) {
	t.Helper()
	rel := model.CatalogRelease{WorkID: workID, Kind: model.ReleaseKindDigital}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeRelease, EntityID: rel.ID, SourceID: source,
		ExternalID: externalID, LinkKind: kind, MatchedBy: "rule:test",
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

// mkDlsiteWork writes one DLsite mirror row; nil pointers leave the key out of
// info_json entirely (the real mirror's "not published" shape).
func mkDlsiteWork(t *testing.T, workno string, star *float64, rc *int, dl, wl *int64, rv *int) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO workratings_dl.works (workno, info_json)
		VALUES (?, jsonb_strip_nulls(jsonb_build_object(
			'rate_average_2dp', ?::float8, 'rate_count', ?::int,
			'dl_count', ?::bigint, 'wishlist_count', ?::bigint, 'review_count', ?::int)))`,
		workno, star, rc, dl, wl, rv).Error)
}

func ratingCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_rating "+where, args...).Scan(&n).Error)
	return n
}

func popCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_popularity "+where, args...).Scan(&n).Error)
	return n
}

func p(v int) *int          { return &v }
func pf(v float64) *float64 { return &v }
func pl(v int64) *int64     { return &v }

// runOpts returns the three-DSN Opts for a test run.
func runOpts(apply bool) Opts {
	return Opts{DSN: testDSN, EGDSN: egTestDSN, DlsiteDSN: dlTestDSN, Apply: apply}
}

// TestBackfillWorkRatings exercises the whole pipeline through the real Run
// entry point: per-lane candidate selection (exact anchors only, bodyless
// only, dlsite release-level + galgame-medium only), the score>0 / NULL-median
// / no-rating filters, vote_count derivation from score_details, rank NULL
// semantics, EG multi-anchor collapse, popularity row planning, dry-run
// zero-write, apply value fidelity, second-pass change-detected no-op, and the
// refresh loop (mutate a mirror value → re-run → row updated).
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

	// --- dlsite lane fixtures (release-level anchors) ---
	wDl := mkWork(t, reg.galgameMedium, "dl-full", nil) // rated + all counters → rating + 3 pop rows
	mkReleaseAnchor(t, wDl, "RJ100001", reg.dlsiteSource, model.LinkKindExact)
	mkDlsiteWork(t, "RJ100001", pf(4.36), p(120), pl(2000), pl(300), p(12))
	wDlNoRating := mkWork(t, reg.galgameMedium, "dl-norating", nil) // no rating, partial counters → 2 pop rows only
	mkReleaseAnchor(t, wDlNoRating, "RJ100002", reg.dlsiteSource, model.LinkKindExact)
	mkDlsiteWork(t, "RJ100002", nil, nil, nil, pl(7), p(0))
	wDlMissing := mkWork(t, reg.galgameMedium, "dl-missing", nil) // absent from mirror
	mkReleaseAnchor(t, wDlMissing, "RJ100003", reg.dlsiteSource, model.LinkKindExact)
	wDlClaimed := mkWork(t, reg.galgameMedium, "dl-claimed", &claimed) // claimed → excluded by SQL
	mkReleaseAnchor(t, wDlClaimed, "RJ100004", reg.dlsiteSource, model.LinkKindExact)
	mkDlsiteWork(t, "RJ100004", pf(4.9), p(999), pl(1), pl(1), p(1))
	wDlAsmr := mkWork(t, 5 /* asmr medium */, "dl-asmr", nil) // wrong medium → excluded (game-domain ruling)
	mkReleaseAnchor(t, wDlAsmr, "RJ100005", reg.dlsiteSource, model.LinkKindExact)
	mkDlsiteWork(t, "RJ100005", pf(4.9), p(999), pl(1), pl(1), p(1))
	wDlProbable := mkWork(t, reg.galgameMedium, "dl-probable", nil) // probable tier → excluded
	mkReleaseAnchor(t, wDlProbable, "RJ100006", reg.dlsiteSource, model.LinkKindProbable)
	mkDlsiteWork(t, "RJ100006", pf(4.0), p(10), pl(1), pl(1), p(1))

	// --- dry run: decides, writes nothing.
	st, err := Run(ctx, runOpts(false))
	require.NoError(t, err)
	assert.Equal(t, 3, st.BgmCandidates, "claimed + probable excluded in SQL")
	assert.Equal(t, 1, st.BgmNoScore)
	assert.Equal(t, 2, st.BgmPlanned, "wBgm + wBgmNoRank")
	assert.Equal(t, 5, st.EgCandidates, "wBgm's EG anchor joins the lane; claimed excluded")
	assert.Equal(t, 1, st.EgMultiAnchor, "wEgMulti's second anchor collapsed")
	assert.Equal(t, 1, st.EgMissingMirror)
	assert.Equal(t, 1, st.EgNoMedian)
	assert.Equal(t, 3, st.EgPlanned, "wEg + wEgMulti + wBgm")
	assert.Equal(t, 3, st.DlCandidates, "claimed / probable / asmr excluded in SQL")
	assert.Equal(t, 1, st.DlMissingMirror)
	assert.Equal(t, 1, st.DlNoRating, "wDlNoRating publishes no rating")
	assert.Equal(t, 1, st.DlRatingPlanned, "wDl")
	assert.Equal(t, 5, st.PopPlanned, "wDl's 3 counters + wDlNoRating's wishlist+reviews")
	assert.Equal(t, 0, st.Refused, "no claimed work reaches the write path")
	assert.Zero(t, st.BgmWritten+st.EgWritten+st.DlRatingWritten+st.PopWritten+
		st.BgmUnchanged+st.EgUnchanged+st.DlRatingUnchanged+st.PopUnchanged+st.Errors)
	assert.EqualValues(t, 0, ratingCount(t, ""), "dry run writes nothing")
	assert.EqualValues(t, 0, popCount(t, ""), "dry run writes nothing")
	require.NotEmpty(t, st.BgmSamples)
	assert.Equal(t, wBgm, st.BgmSamples[0].WorkID)
	assert.Equal(t, 42, st.BgmSamples[0].VoteCount, "vote_count = summed score_details buckets")
	require.NotEmpty(t, st.DlSamples)
	assert.Equal(t, "RJ100001", st.DlSamples[0].Workno)

	// --- apply: writes the decided plan exactly.
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 2, st.BgmWritten)
	assert.Equal(t, 3, st.EgWritten)
	assert.Equal(t, 1, st.DlRatingWritten)
	assert.Equal(t, 5, st.PopWritten)
	assert.Zero(t, st.BgmUnchanged+st.EgUnchanged+st.DlRatingUnchanged+st.PopUnchanged+st.Errors)
	assert.EqualValues(t, 6, ratingCount(t, ""))
	assert.EqualValues(t, 5, popCount(t, ""))

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

	// The dlsite rating row (native 0-5 star average, rank always NULL).
	var rDl model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wDl, reg.dlsiteSource).First(&rDl).Error)
	assert.InDelta(t, 4.36, rDl.Score, 1e-9)
	assert.Equal(t, 120, rDl.VoteCount)
	assert.Nil(t, rDl.Rank)

	// The popularity rows: full trio on wDl…
	var pops []model.CatalogWorkPopularity
	require.NoError(t, testDB.Where("work_id = ?", wDl).Order("metric").Find(&pops).Error)
	require.Len(t, pops, 3)
	assert.Equal(t, model.PopularityMetricDownloads, pops[0].Metric)
	assert.EqualValues(t, 2000, pops[0].Value)
	assert.Equal(t, model.PopularityMetricWishlist, pops[1].Metric)
	assert.EqualValues(t, 300, pops[1].Value)
	assert.Equal(t, model.PopularityMetricReviews, pops[2].Metric)
	assert.EqualValues(t, 12, pops[2].Value)
	assert.Equal(t, reg.dlsiteSource, pops[0].SourceID)
	// …and only the PUBLISHED counters on wDlNoRating (absent dl_count → no
	// row; published review_count 0 → a real 0-valued row).
	require.NoError(t, testDB.Where("work_id = ?", wDlNoRating).Order("metric").Find(&pops).Error)
	require.Len(t, pops, 2)
	assert.Equal(t, model.PopularityMetricWishlist, pops[0].Metric)
	assert.EqualValues(t, 7, pops[0].Value)
	assert.Equal(t, model.PopularityMetricReviews, pops[1].Metric)
	assert.EqualValues(t, 0, pops[1].Value)

	// Both lanes on one work → two rows, one per source.
	assert.EqualValues(t, 2, ratingCount(t, "WHERE work_id = ?", wBgm))
	assert.EqualValues(t, 0, ratingCount(t, "WHERE work_id IN (?, ?, ?)", wBgmClaimed, wEgClaimed, wDlClaimed),
		"claimed works never materialise")
	assert.EqualValues(t, 0, popCount(t, "WHERE work_id IN (?, ?)", wDlClaimed, wDlAsmr),
		"claimed/off-domain works never materialise")

	// --- second apply: change-detected no-op — zero effective writes, every
	// planned row unchanged, no row growth.
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Zero(t, st.BgmWritten+st.EgWritten+st.DlRatingWritten+st.PopWritten+st.Errors, "second pass writes zero")
	assert.Equal(t, 2, st.BgmUnchanged)
	assert.Equal(t, 3, st.EgUnchanged)
	assert.Equal(t, 1, st.DlRatingUnchanged)
	assert.Equal(t, 5, st.PopUnchanged)
	assert.EqualValues(t, 6, ratingCount(t, ""), "row count unchanged")
	assert.EqualValues(t, 5, popCount(t, ""), "row count unchanged")

	// --- refresh loop (step 62 ⑤): mutate mirror values → third apply updates
	// exactly those rows in place.
	require.NoError(t, testDB.Exec(
		`UPDATE workratings_dl.works SET info_json = info_json || '{"wishlist_count": 301, "rate_count": 121}' WHERE workno = 'RJ100001'`).Error)
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 1, st.DlRatingWritten, "rate_count change updates the rating row")
	assert.Equal(t, 1, st.PopWritten, "exactly the mutated wishlist row updates")
	assert.Equal(t, 4, st.PopUnchanged)
	assert.Zero(t, st.BgmWritten+st.EgWritten, "untouched lanes stay no-op")
	assert.EqualValues(t, 6, ratingCount(t, ""), "update in place — no row growth")
	assert.EqualValues(t, 5, popCount(t, ""))
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wDl, reg.dlsiteSource).First(&rDl).Error)
	assert.Equal(t, 121, rDl.VoteCount, "refreshed vote_count lands")
	var wl model.CatalogWorkPopularity
	require.NoError(t, testDB.Where("work_id = ? AND metric = ?", wDl, model.PopularityMetricWishlist).First(&wl).Error)
	assert.EqualValues(t, 301, wl.Value, "refreshed wishlist lands")
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

	// XOR: a claimed row is refused before any write (both facets).
	w := &writer{db: testDB, stats: &Stats{}}
	var written, unchanged int
	w.write(ctx, plannedRow{WorkID: wClaimed, Site: &claimed, SourceID: reg.bangumiSource, Score: 7.0}, true, &written, &unchanged)
	w.writePopularity(ctx, popPlannedRow{WorkID: wClaimed, Site: &claimed, SourceID: reg.dlsiteSource,
		Metric: model.PopularityMetricDownloads, Value: 5}, true)
	assert.Equal(t, 2, w.stats.Refused)
	assert.Zero(t, written+unchanged+w.stats.PopWritten+w.stats.PopUnchanged)
	assert.EqualValues(t, 0, ratingCount(t, ""))
	assert.EqualValues(t, 0, popCount(t, ""))

	// A bodyless row writes; a same-value retry is a change-detected no-op; a
	// changed-value retry UPDATEs in place (the step-62 upsert unification).
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.bangumiSource, Score: 7.0, VoteCount: 3}, true, &written, &unchanged)
	assert.Equal(t, 1, written)
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.bangumiSource, Score: 7.0, VoteCount: 3}, true, &written, &unchanged)
	assert.Equal(t, 1, unchanged, "unchanged values → no-op")
	w.write(ctx, plannedRow{WorkID: wBody, Site: nil, SourceID: reg.bangumiSource, Score: 7.2, VoteCount: 4}, true, &written, &unchanged)
	assert.Equal(t, 2, written, "changed values → in-place update")
	assert.EqualValues(t, 1, ratingCount(t, ""), "still exactly one row")
	var r model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ?", wBody).First(&r).Error)
	assert.InDelta(t, 7.2, r.Score, 1e-9)

	// Same trio for popularity.
	w.writePopularity(ctx, popPlannedRow{WorkID: wBody, Site: nil, SourceID: reg.dlsiteSource,
		Metric: model.PopularityMetricWishlist, Value: 10}, true)
	w.writePopularity(ctx, popPlannedRow{WorkID: wBody, Site: nil, SourceID: reg.dlsiteSource,
		Metric: model.PopularityMetricWishlist, Value: 10}, true)
	w.writePopularity(ctx, popPlannedRow{WorkID: wBody, Site: nil, SourceID: reg.dlsiteSource,
		Metric: model.PopularityMetricWishlist, Value: 11}, true)
	assert.Equal(t, 2, w.stats.PopWritten)
	assert.Equal(t, 1, w.stats.PopUnchanged)
	assert.EqualValues(t, 1, popCount(t, ""), "still exactly one row")
	var pop model.CatalogWorkPopularity
	require.NoError(t, testDB.Where("work_id = ?", wBody).First(&pop).Error)
	assert.EqualValues(t, 11, pop.Value)

	// DSN discipline: all three DSNs are required, never guessed.
	_, err = Run(ctx, Opts{EGDSN: testDSN, DlsiteDSN: testDSN})
	require.Error(t, err)
	_, err = Run(ctx, Opts{DSN: testDSN, DlsiteDSN: testDSN})
	require.Error(t, err)
	_, err = Run(ctx, Opts{DSN: testDSN, EGDSN: testDSN})
	require.Error(t, err)
}

// TestNonTitleYearExactAnchorEntersBgmCandidates pins the step-79 fix: an EXACT
// Bangumi work anchor whose matched_by is NOT rule:bgm-title-year (here the
// wave-78 rule:bgm-type4-gated tier) now enters the bangumi-lane candidate set.
// Before the fix the hard-coded matched_by filter left the 11,465 new anchors
// invisible. The exact gate is unchanged: a probable anchor still stays out.
func TestNonTitleYearExactAnchorEntersBgmCandidates(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	wGated := mkWork(t, reg.galgameMedium, "bgm-gated", nil)
	mkSubject(t, 601, 7.4, 321, `{"7":20,"10":12}`) // votes 32, score>0 → a rating row
	mkAnchor(t, wGated, "601", reg.bangumiSource, model.LinkKindExact, "rule:bgm-type4-gated")

	wProbable := mkWork(t, reg.galgameMedium, "bgm-probable", nil)
	mkSubject(t, 602, 8.0, 1, `{"10":5}`)
	mkAnchor(t, wProbable, "602", reg.bangumiSource, model.LinkKindProbable, "rule:bgm-title-only")

	st, err := Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 1, st.BgmCandidates, "the gated-rule exact anchor is now a bgm candidate; probable stays out")
	assert.EqualValues(t, 1, ratingCount(t, "WHERE work_id = ? AND source_id = ?", wGated, reg.bangumiSource))
	assert.EqualValues(t, 0, ratingCount(t, "WHERE work_id = ?", wProbable))
}
