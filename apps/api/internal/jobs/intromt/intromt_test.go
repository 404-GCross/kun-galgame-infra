package intromt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the catalog Gold schema
// (migrate.Run + seeds), exactly as production lays it out. Run drives the DSN
// itself, so we capture it (not just the handle) to exercise the real entry
// point.
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
	testDB = db
	os.Exit(m.Run())
}

// --- fixtures -------------------------------------------------------------

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{"catalog_work_intro", "catalog_work_popularity", "catalog_work"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mkWork(t *testing.T, medium int16, name string, site *string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name, Site: site}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

// mkIntro inserts a SOURCE intro row (provenance 0).
func mkIntro(t *testing.T, workID int64, lang, intro string, source int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkIntro{
		WorkID: workID, Lang: lang, Intro: intro, SourceID: source, Provenance: 0,
	}).Error)
}

func mkPop(t *testing.T, workID int64, source, metric int16, value int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkPopularity{
		WorkID: workID, SourceID: source, Metric: metric, Value: value,
	}).Error)
}

func introCount(t *testing.T, where string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_work_intro "+where, args...).Scan(&n).Error)
	return n
}

func reg(t *testing.T) (medium, dlsite, bangumi int16) {
	t.Helper()
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key='dlsite'`).Scan(&dlsite).Error)
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key='bangumi'`).Scan(&bangumi).Error)
	require.NotZero(t, medium)
	require.NotZero(t, dlsite)
	require.NotZero(t, bangumi)
	return
}

// fakeTranslator is the offline LLM seam for pipeline tests: deterministic,
// counts calls (to prove no LLM is dialed in dry / on skips).
type fakeTranslator struct {
	model string
	calls int
	fn    func(ja string) string
}

func (f *fakeTranslator) Translate(_ context.Context, ja string) (string, string, error) {
	f.calls++
	return f.fn(ja), f.model, nil
}

// --- tests ----------------------------------------------------------------

// TestPilotEndToEnd drives the whole pipeline through the real Run entry point:
// candidate selection (bodyless + has-ja + no-zh-source, popularity ordered),
// dry forecast with NO LLM call, apply insert, second-pass idempotence,
// hash-change re-translate, and the never-overwrite exclusion.
func TestPilotEndToEnd(t *testing.T) {
	clean(t)
	ctx := context.Background()
	medium, dlsite, bangumi := reg(t)
	claimed := "galgame_wiki"

	wInsert := mkWork(t, medium, "ja-no-zh", nil)      // ja intro, no zh → insert
	wHasZh := mkWork(t, medium, "ja-has-zh-src", nil)  // ja + zh source → excluded (fill-missing)
	wClaimed := mkWork(t, medium, "claimed", &claimed) // claimed → excluded (bodyless only)
	wNoJa := mkWork(t, medium, "no-ja", nil)           // no ja intro → not a candidate

	mkIntro(t, wInsert, "ja", "これはあらすじです。", bangumi)
	mkIntro(t, wHasZh, "ja", "日本語のあらすじ。", bangumi)
	mkIntro(t, wHasZh, "zh-Hans", "已有的中文简介。", bangumi) // source zh → excludes the work
	mkIntro(t, wClaimed, "ja", "claimed ja", bangumi)
	mkIntro(t, wNoJa, "en", "english only", bangumi)

	// popularity so ordering is exercised
	mkPop(t, wInsert, dlsite, model.PopularityMetricDownloads, 500)

	tr := &fakeTranslator{model: "test-mt", fn: func(ja string) string { return "[译] " + ja }}

	// --- dry: forecasts, NO LLM, NO write.
	st, err := Run(ctx, nil, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Candidates, "only wInsert qualifies (has ja, no zh source, bodyless)")
	assert.Equal(t, 1, st.WouldInsert)
	assert.Equal(t, 0, st.WouldRetranslate)
	assert.Equal(t, 0, st.Inserted, "dry writes nothing")
	assert.EqualValues(t, 5, introCount(t, ""), "dry writes nothing (5 intro fixtures)")
	require.Len(t, st.Samples, 1)
	assert.Empty(t, st.Samples[0].Zh, "dry captures ja only, no translation")

	// --- apply: translates + inserts one machine row.
	st, err = Run(ctx, tr, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Inserted)
	assert.Equal(t, 0, st.Refused)
	assert.Equal(t, 0, st.Errors)
	assert.Equal(t, 1, tr.calls, "exactly one translation (the single candidate)")

	var row model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id=? AND lang='zh-Hans'", wInsert).First(&row).Error)
	assert.EqualValues(t, 1, row.Provenance, "machine row")
	assert.Equal(t, bangumi, row.SourceID, "attributed to the source ja row's source_id")
	assert.Equal(t, "[译] これはあらすじです。", row.Intro)
	assert.Equal(t, hashSource("これはあらすじです。"), row.SrcHash)
	assert.Equal(t, "test-mt", row.MTModel)
	assert.EqualValues(t, 0, introCount(t, "WHERE work_id=? AND lang='zh-Hans'", wClaimed), "claimed work gets no machine row")
	assert.EqualValues(t, 6, introCount(t, ""), "5 fixtures + 1 machine insert")

	// --- second apply: idempotent (hash unchanged) — zero writes, zero LLM.
	tr2 := &fakeTranslator{model: "test-mt", fn: func(ja string) string { return "SHOULD-NOT-BE-CALLED" }}
	st, err = Run(ctx, tr2, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.SkipUnchanged)
	assert.Zero(t, st.Inserted+st.Retranslated)
	assert.Equal(t, 0, tr2.calls, "unchanged source → no LLM call")

	// --- hash change: mutate the ja source text → re-translate (upsert), same key.
	require.NoError(t, testDB.Exec(`UPDATE catalog_work_intro SET intro=? WHERE work_id=? AND lang='ja'`,
		"あらすじが変わった。", wInsert).Error)
	tr3 := &fakeTranslator{model: "test-mt-v2", fn: func(ja string) string { return "[新译] " + ja }}
	st, err = Run(ctx, tr3, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.WouldRetranslate)
	assert.Equal(t, 1, st.Retranslated)
	assert.Equal(t, 1, tr3.calls)
	var updated model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id=? AND lang='zh-Hans'", wInsert).First(&updated).Error)
	assert.Equal(t, "[新译] あらすじが変わった。", updated.Intro, "re-translated in place")
	assert.Equal(t, hashSource("あらすじが変わった。"), updated.SrcHash, "src_hash follows the new source")
	assert.Equal(t, "test-mt-v2", updated.MTModel)
	assert.EqualValues(t, 1, introCount(t, "WHERE work_id=? AND lang='zh-Hans'", wInsert), "still one zh-Hans row (upsert, not a second row)")
}

// TestNeverOverwriteSource proves a machine write can NEVER clobber a source
// row sharing the (work_id, lang, source_id) key: the guarded upsert reports
// zero rows affected and the run counts it Refused.
func TestNeverOverwriteSource(t *testing.T) {
	clean(t)
	ctx := context.Background()
	medium, _, bangumi := reg(t)
	w := mkWork(t, medium, "src-zh-guard", nil)
	// A SOURCE zh-Hans row at the exact key a machine row would target.
	mkIntro(t, w, "zh-Hans", "人工/源中文,不可覆盖。", bangumi)

	r := &runner{db: testDB, tr: nil, stats: &Stats{}}
	rows, err := r.upsert(ctx, candidate{WorkID: w, JaSourceID: bangumi}, "机翻不该落地", "deadbeef", "test-mt")
	require.NoError(t, err)
	assert.EqualValues(t, 0, rows, "DO UPDATE WHERE provenance=1 refuses the source row")

	var row model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id=? AND lang='zh-Hans'", w).First(&row).Error)
	assert.Equal(t, "人工/源中文,不可覆盖。", row.Intro, "source text intact")
	assert.EqualValues(t, 0, row.Provenance, "still a source row")
}

// TestPopularityOrdering pins the deterministic rank: downloads preferred, then
// wishlist, then 0, work_id ASC tiebreak. --limit takes the most-popular N.
func TestPopularityOrdering(t *testing.T) {
	clean(t)
	ctx := context.Background()
	medium, dlsite, bangumi := reg(t)

	wHi := mkWork(t, medium, "hi-downloads", nil)
	wMid := mkWork(t, medium, "mid-downloads", nil)
	wWish := mkWork(t, medium, "wishlist-only", nil)
	for _, w := range []int64{wHi, wMid, wWish} {
		mkIntro(t, w, "ja", fmt.Sprintf("あらすじ%d", w), bangumi)
	}
	mkPop(t, wHi, dlsite, model.PopularityMetricDownloads, 9000)
	mkPop(t, wMid, dlsite, model.PopularityMetricDownloads, 100)
	mkPop(t, wWish, dlsite, model.PopularityMetricWishlist, 8000) // no downloads → wishlist fallback

	reg2, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)
	cands, err := loadCandidates(ctx, testDB, reg2, PopulationBodyless, 5000, 2)
	require.NoError(t, err)
	require.Len(t, cands, 2, "--limit 2 keeps the two most popular")
	assert.Equal(t, wHi, cands[0].WorkID, "9000 downloads first")
	// wWish (wishlist 8000) outranks wMid (downloads 100) under COALESCE(dl,wl).
	assert.Equal(t, wWish, cands[1].WorkID)
}

// TestClaimedPopulation drives the claimed lane (site='galgame_wiki'): wiki
// works with a step-q ja row (source_id=galgame_wiki) qualify, bodyless works
// are excluded, fill-missing still applies, and the machine row lands
// attributed to the wiki ja row's source_id. Ordering falls back to work_id
// ASC (claimed works carry no dlsite popularity).
func TestClaimedPopulation(t *testing.T) {
	clean(t)
	ctx := context.Background()
	medium, _, bangumi := reg(t)
	var wiki int16
	// Dual-read: wave 161 renamed source 12's key galgame_wiki → curated, and
	// the fixture must resolve the same row on either side of that deploy.
	require.NoError(t, testDB.Raw(
		`SELECT id FROM catalog_source WHERE key IN ('curated','galgame_wiki') ORDER BY id LIMIT 1`).
		Scan(&wiki).Error)
	require.NotZero(t, wiki)
	claimed := "galgame_wiki"

	wA := mkWork(t, medium, "claimed-a", &claimed)
	wB := mkWork(t, medium, "claimed-b", &claimed)
	wZh := mkWork(t, medium, "claimed-has-zh", &claimed)
	wBodyless := mkWork(t, medium, "bodyless", nil)

	mkIntro(t, wA, "ja", "認領作品Aのあらすじ。", wiki)
	mkIntro(t, wB, "ja", "認領作品Bのあらすじ。", wiki)
	mkIntro(t, wZh, "ja", "中文既存のあらすじ。", wiki)
	mkIntro(t, wZh, "zh-Hans", "已有的中文简介。", bangumi) // any zh source row excludes the work
	mkIntro(t, wBodyless, "ja", "ボディレスのあらすじ。", bangumi)

	// dry claimed lane: exactly the two zh-less wiki works, work_id ASC.
	st, err := Run(ctx, nil, Opts{DSN: testDSN, Population: PopulationClaimed})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Candidates, "wZh excluded (fill-missing), wBodyless excluded (claimed lane)")

	reg2, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)
	cands, err := loadCandidates(ctx, testDB, reg2, PopulationClaimed, 5000, 0)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	assert.Equal(t, wA, cands[0].WorkID, "no popularity → work_id ASC")
	assert.Equal(t, wB, cands[1].WorkID)
	assert.Equal(t, wiki, cands[0].JaSourceID, "chosen ja row is the wiki materialized one")

	// apply: machine rows land on the wiki works only, wiki attribution.
	tr := &fakeTranslator{model: "claimed-mt", fn: func(ja string) string { return "[译] " + ja }}
	st, err = Run(ctx, tr, Opts{DSN: testDSN, Apply: true, Population: PopulationClaimed})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Inserted)
	assert.Zero(t, st.Refused)
	assert.Zero(t, st.Errors)

	var row model.CatalogWorkIntro
	require.NoError(t, testDB.Where("work_id=? AND lang='zh-Hans'", wA).First(&row).Error)
	assert.EqualValues(t, 1, row.Provenance, "machine row")
	assert.Equal(t, wiki, row.SourceID, "attributed to the wiki ja row's source_id")
	assert.Equal(t, "[译] 認領作品Aのあらすじ。", row.Intro)
	assert.EqualValues(t, 0, introCount(t, "WHERE work_id=? AND lang='zh-Hans'", wBodyless), "bodyless work untouched by the claimed lane")

	// default lane stays bodyless: the same DB state yields only wBodyless.
	st, err = Run(ctx, nil, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Candidates, "empty Population defaults to the bodyless pilot lane")

	// unknown population → hard error, nothing guessed.
	_, err = Run(ctx, nil, Opts{DSN: testDSN, Population: "everything"})
	assert.ErrorContains(t, err, "unknown population")
}

// TestHTTPTranslator is the LLM-call-layer mock gate (httptest): the client
// posts the pinned system prompt + ja user text with a bearer token to
// {base}/chat/completions and parses the plain-text reply as the translation.
func TestHTTPTranslator(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = io.WriteString(w, `{"model":"deepseek-chat","choices":[{"message":{"role":"assistant","content":"  这是译文。  "},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	tr := NewHTTPTranslator(srv.URL, "sekret", "deepseek-chat", 512)
	require.True(t, tr.Configured())
	zh, model, err := tr.Translate(context.Background(), "これは原文です。")
	require.NoError(t, err)
	assert.Equal(t, "这是译文。", zh, "content trimmed, plain text is the translation")
	assert.Equal(t, "deepseek-chat", model)
	assert.Equal(t, "Bearer sekret", gotAuth)
	assert.Equal(t, "/chat/completions", gotPath)
	require.Len(t, gotBody.Messages, 2)
	assert.Equal(t, TranslateSystemPrompt, gotBody.Messages[0].Content, "pinned system prompt sent verbatim")
	assert.Equal(t, "これは原文です。", gotBody.Messages[1].Content, "ja source as user content")
	assert.EqualValues(t, 0, gotBody.Temperature, "faithful/deterministic")
}

// TestHTTPTranslatorErrors: a 5xx and an empty-choices reply both surface as
// errors (the runner records them and continues — never writes a bad row).
func TestHTTPTranslatorErrors(t *testing.T) {
	// Shrink the retry backoff for the whole test — the 502 case below burns
	// the full real schedule (40s) otherwise.
	origSchedule := retrySchedule
	retrySchedule = []time.Duration{time.Millisecond}
	defer func() { retrySchedule = origSchedule }()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "boom")
	}))
	defer bad.Close()
	tr := NewHTTPTranslator(bad.URL, "t", "m", 64)
	_, _, err := tr.Translate(context.Background(), "x")
	assert.Error(t, err)

	// 429 then 200: the retry valve rides out a rate-limit burst instead of
	// recording an error (worker-pool safety).
	var hits int
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"role":"assistant","content":"译"},"finish_reason":"stop"}]}`)
	}))
	defer limited.Close()
	zh, _, err := NewHTTPTranslator(limited.URL, "t", "m", 64).Translate(context.Background(), "x")
	require.NoError(t, err, "429 retried to success")
	assert.Equal(t, "译", zh)
	assert.Equal(t, 2, hits)

	// finish_reason=length: a reasoning model squeezed by max_tokens emits a
	// non-empty PARTIAL — must error, never be returned as a translation.
	trunc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"role":"assistant","content":"残りは途中で"},"finish_reason":"length"}]}`)
	}))
	defer trunc.Close()
	_, _, err = NewHTTPTranslator(trunc.URL, "t", "m", 64).Translate(context.Background(), "x")
	assert.ErrorContains(t, err, "finish_reason", "partial output refused")

	assert.False(t, NewHTTPTranslator("", "t", "m", 64).Configured(), "no base → not configured")
	assert.False(t, NewHTTPTranslator("http://x/v1", "", "m", 64).Configured(), "no token → not configured")
}

// TestConcurrentApply: the worker pool writes every candidate exactly once
// with consistent stats — same outcome as serial, only faster. The fake
// translator sleeps a hair so workers genuinely overlap (race detector food).
func TestConcurrentApply(t *testing.T) {
	clean(t)
	ctx := context.Background()
	medium, dlsite, bangumi := reg(t)

	const n = 40
	for i := range n {
		w := mkWork(t, medium, fmt.Sprintf("conc-%d", i), nil)
		mkIntro(t, w, "ja", fmt.Sprintf("あらすじ %d。", i), bangumi)
		mkPop(t, w, dlsite, model.PopularityMetricDownloads, int64(1000-i))
	}

	tr := &slowFakeTranslator{model: "conc-mt"}
	st, err := Run(ctx, tr, Opts{DSN: testDSN, Apply: true, Workers: 8})
	require.NoError(t, err)
	assert.Equal(t, n, st.Candidates)
	assert.Equal(t, n, st.Inserted)
	assert.Zero(t, st.Errors)
	assert.Zero(t, st.Refused)
	assert.EqualValues(t, n, introCount(t, "WHERE provenance = 1"))
	assert.EqualValues(t, n, tr.calls.Load(), "one gateway call per candidate")

	// Second concurrent pass: pure idempotent skip, no LLM calls.
	st, err = Run(ctx, tr, Opts{DSN: testDSN, Apply: true, Workers: 8})
	require.NoError(t, err)
	assert.Equal(t, n, st.SkipUnchanged)
	assert.Zero(t, st.Inserted)
	assert.EqualValues(t, n, tr.calls.Load(), "skips never dial the gateway")
}

// slowFakeTranslator is a thread-safe fake with a tiny latency so the pool
// actually overlaps.
type slowFakeTranslator struct {
	model string
	calls atomic.Int64
}

func (f *slowFakeTranslator) Translate(_ context.Context, ja string) (string, string, error) {
	f.calls.Add(1)
	time.Sleep(2 * time.Millisecond)
	return "[译] " + ja, f.model, nil
}

// TestMockTranslatorDeterminism: the rehearsal mock is a pure function of the
// source (idempotence + re-translate proof) and stamps an obvious mock model.
func TestMockTranslatorDeterminism(t *testing.T) {
	m := MockTranslator{Model: "stub"}
	a1, mdl, err := m.Translate(context.Background(), "同じ原文")
	require.NoError(t, err)
	a2, _, _ := m.Translate(context.Background(), "同じ原文")
	b, _, _ := m.Translate(context.Background(), "違う原文")
	assert.Equal(t, a1, a2, "same source → same output (idempotent)")
	assert.NotEqual(t, a1, b, "different source → different output (re-translate trigger)")
	assert.True(t, strings.HasPrefix(mdl, "mock:"), "obvious mock model id")
}
