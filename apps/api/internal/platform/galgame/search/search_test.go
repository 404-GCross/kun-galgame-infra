package search

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/infrastructure/search"
	"api/pkg/config"

	"github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests against a real Meilisearch instance.
// Set MEILISEARCH_TEST_HOST to override (default 127.0.0.1:7700).
// Tests skip if MS is unreachable.

var (
	testClient *search.Client
	testSvc    *Service
	testIdxer  *Indexer
)

const testIndexPrefix = "test_" // every test uses this prefix so it doesn't touch prod/dev indexes

func TestMain(m *testing.M) {
	host := os.Getenv("MEILISEARCH_TEST_HOST")
	if host == "" {
		host = "http://127.0.0.1:7700"
	}

	client, err := search.NewClient(config.MeilisearchConfig{
		Host:        host,
		APIKey:      os.Getenv("MEILISEARCH_TEST_API_KEY"),
		IndexPrefix: testIndexPrefix,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: meilisearch client init failed: %v\n", err)
		os.Exit(0)
	}
	if err := client.Health(); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: meilisearch unreachable at %s: %v\n", host, err)
		os.Exit(0)
	}

	// Wipe any leftover test indexes from previous runs.
	for _, uid := range []string{IndexGalgames, IndexTags, IndexOfficials} {
		_, _ = client.Svc().DeleteIndex(client.IndexUID(uid))
	}
	time.Sleep(300 * time.Millisecond) // give MS time to process deletes

	if err := EnsureIndexes(client); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: EnsureIndexes failed: %v\n", err)
		os.Exit(0)
	}

	testClient = client
	testIdxer = NewIndexer(client)
	testSvc = NewService(client)

	code := m.Run()

	// Cleanup test indexes on exit
	for _, uid := range []string{IndexGalgames, IndexTags, IndexOfficials} {
		_, _ = client.Svc().DeleteIndex(client.IndexUID(uid))
	}

	os.Exit(code)
}

// waitForTasks blocks until all in-flight indexing tasks on an index complete.
// Meilisearch indexing is async, so we need this before asserting search results.
func waitForTasks(t *testing.T, indexUID string) {
	t.Helper()
	idx := testClient.Index(indexUID)

	// Poll tasks for up to 10 seconds
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := idx.GetTasks(&meilisearch.TasksQuery{Statuses: []meilisearch.TaskStatus{"enqueued", "processing"}})
		if err != nil {
			t.Fatalf("poll tasks: %v", err)
		}
		if len(tasks.Results) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for MS tasks")
}

// clearIndex removes all documents from an index (keeping the index + settings).
func clearIndex(t *testing.T, indexUID string) {
	t.Helper()
	_, err := testClient.Index(indexUID).DeleteAllDocuments(nil)
	require.NoError(t, err)
	waitForTasks(t, indexUID)
}

// ═══════════════════════════════════════════════════════════════════
// Pure helpers
// ═══════════════════════════════════════════════════════════════════

func TestParseReleased(t *testing.T) {
	tests := []struct {
		in       string
		wantYear int
		wantOK   bool
	}{
		{"2020", 2020, true},
		{"2020-05", 2020, true},
		{"2020-05-15", 2020, true},
		{"unknown", 0, false},
		{"tba", 0, false},
		{"", 0, false},
		{"not-a-date", 0, false},
		{"1800", 0, false},         // too early
		{"2200", 0, false},         // too late
		{"2020-13-40", 2020, true}, // invalid month/day still returns year with defaults
	}
	for _, tt := range tests {
		y, _, ok := parseReleased(tt.in)
		assert.Equal(t, tt.wantOK, ok, "in=%q", tt.in)
		if ok {
			assert.Equal(t, tt.wantYear, y, "in=%q", tt.in)
		}
	}
}

// TestSanitizeQuery locks in the neutralization of Meilisearch's query-string
// operators (see sanitizeQuery). The regression that motivated it: VNDB's
// "-サブタイトル-" title convention — pasted verbatim, the leading '-' was parsed
// as NEGATION and excluded the game from its own name search.
func TestSanitizeQuery(t *testing.T) {
	cases := map[string]string{
		// the reported bug: dash-delimited subtitle must not negate
		"CRAZY CHA!N -エルピスの鎖-": "CRAZY CHA!N  エルピスの鎖",
		"-エルピス":                "エルピス",        // bare leading-dash negation neutralized
		`"CLANNAD"`:            "CLANNAD",     // phrase quotes stripped
		"Steins-Gate":          "Steins Gate", // internal dash → space (tokenizer splits it anyway)
		// untouched cases
		"CLANNAD":    "CLANNAD",
		"ファントム":      "ファントム",
		"":           "",
		"  spaced  ": "spaced",
		// Japanese long-vowel mark (U+30FC) is a letter, NOT ASCII '-' — keep it
		"ソードアート": "ソードアート",
	}
	for in, want := range cases {
		assert.Equal(t, want, sanitizeQuery(in), "in=%q", in)
	}
}

func TestBuildGalgameFilter(t *testing.T) {
	seven := 7
	req := &GalgameSearchRequest{
		Statuses:          []int{0, 2},
		ContentLimit:      "sfw",
		TagIDs:            []int{1, 2},
		OriginalLanguages: []string{"ja-jp", "en-us"},
		SeriesID:          &seven,
	}
	filter := buildGalgameFilter(req)
	// Check key substrings — order is deterministic (built sequentially in function)
	assert.Contains(t, filter, "status IN [0, 2]")
	// NSFW (content_limit) is intentionally NOT filtered in search, even when the
	// request carries a ContentLimit — search surfaces all games regardless of
	// NSFW (SEO is handled by page-level noindex). See buildGalgameFilter.
	assert.NotContains(t, filter, "content_limit")
	assert.Contains(t, filter, "tag_ids = 1")
	assert.Contains(t, filter, "tag_ids = 2")
	assert.Contains(t, filter, "original_language IN ['ja-jp', 'en-us']")
	assert.Contains(t, filter, "series_id = 7")
}

func TestEscapeFilter(t *testing.T) {
	assert.Equal(t, `it\'s`, escapeFilter("it's"))
	assert.Equal(t, `a\\b`, escapeFilter(`a\b`))
	assert.Equal(t, `normal`, escapeFilter("normal"))
}

func TestGalgameSortRule(t *testing.T) {
	assert.Equal(t, "", galgameSortRule(""))
	assert.Equal(t, "", galgameSortRule("relevance"))
	assert.Equal(t, "released_ts:desc", galgameSortRule("released_desc"))
	assert.Equal(t, "released_ts:asc", galgameSortRule("released_asc"))
	assert.Equal(t, "view:desc", galgameSortRule("view"))
	assert.Equal(t, "updated_ts:desc", galgameSortRule("updated"))
	assert.Equal(t, "", galgameSortRule("bogus"))
}

// ═══════════════════════════════════════════════════════════════════
// Galgame search
// ═══════════════════════════════════════════════════════════════════

func seedGalgames(t *testing.T, docs ...*GalgameDoc) {
	t.Helper()
	_, err := testClient.Index(IndexGalgames).AddDocuments(docs, nil)
	require.NoError(t, err)
	waitForTasks(t, IndexGalgames)
}

func newGalgameDoc(id int, vndbID, jaName, enName string) *GalgameDoc {
	return &GalgameDoc{
		ID:               id,
		VNDBID:           vndbID,
		NameJaJP:         jaName,
		NameEnUS:         enName,
		Aliases:          []string{},
		TagNames:         []string{},
		TagIDs:           []int{},
		OfficialNames:    []string{},
		OfficialIDs:      []int{},
		EngineNames:      []string{},
		EngineIDs:        []int{},
		ContentLimit:     "sfw",
		AgeLimit:         "r18",
		OriginalLanguage: "ja-jp",
		Status:           0,
		View:             100,
		CreatedTS:        time.Now().Unix(),
		UpdatedTS:        time.Now().Unix(),
	}
}

func TestGalgameSearch_ByName(t *testing.T) {
	clearIndex(t, IndexGalgames)
	seedGalgames(t,
		newGalgameDoc(1, "v100", "ファントム", "Phantom of Inferno"),
		newGalgameDoc(2, "v200", "CLANNAD", "CLANNAD"),
		newGalgameDoc(3, "v300", "AIR", "AIR"),
	)

	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Query: "CLANNAD",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Len(t, resp.Items, 1)
	assert.EqualValues(t, 2, resp.Items[0]["id"])
}

// newGalgameDocZh builds a doc with a Chinese (zh_cn) name. The CJK cases
// need name_zh_cn populated — newGalgameDoc only sets ja/en.
func newGalgameDocZh(id int, vndbID, zhName string) *GalgameDoc {
	d := newGalgameDoc(id, vndbID, "", "")
	d.NameZhCN = zhName
	return d
}

// TestGalgameSearch_CJK_NoCoTokenNoise locks in the fix for the reported bug
// (search "时间奏响的" / "双子迷宫" → flood of unrelated titles). A partial /
// absent Chinese title query used to surface every title sharing ONE common
// token (时间 / 双子) because MatchingStrategy=Frequency dropped the rare term
// and the 0.4 score floor didn't catch single-common-token hits. The fix is
// two-part and this exercises both:
//   - MatchingStrategy=All (service.go): a doc must contain ALL query tokens,
//     so the decoys that lack the distinguishing token (迷宫) are excluded.
//   - StopWords 的 (settings.go): the possessive particle is dropped from the
//     query, so a legitimate title that omits 的 still matches.
//
// Decoys deliberately lack the distinguishing token, so the assertions hold
// whether Meilisearch segments at word level (jieba) or per character.
func TestGalgameSearch_CJK_NoCoTokenNoise(t *testing.T) {
	clearIndex(t, IndexGalgames)
	seedGalgames(t,
		newGalgameDocZh(9001, "v9001", "魔法学院"), // decoy: shares 学院, no 迷宫
		newGalgameDocZh(9002, "v9002", "贵族学院"), // decoy: shares 学院, no 迷宫
		newGalgameDocZh(9003, "v9003", "学院迷宫"), // target: 学院 + 迷宫
	)

	// (A) Multi-token query resolves ONLY to the title containing every token.
	// Pre-fix this returned all three (shared 学院); now the 迷宫-less decoys
	// are gone.
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "学院迷宫"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total, "multi-token CJK query must not surface co-token noise")
	require.Len(t, resp.Items, 1)
	assert.EqualValues(t, 9003, resp.Items[0]["id"])

	// (C) The stop word 的 is dropped, so "学院的迷宫" still matches "学院迷宫"
	// (which has no 的) — the particle is never a spuriously-required term.
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "学院的迷宫"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total, "stop word 的 must not be a required term")
	require.Len(t, resp.Items, 1)
	assert.EqualValues(t, 9003, resp.Items[0]["id"])

	// Recall preserved: an exact full title still resolves to itself.
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "魔法学院"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, resp.Total, int64(1))
	assert.EqualValues(t, 9001, resp.Items[0]["id"])

	// A wholly-absent title returns nothing rather than fuzzy noise — the
	// "双子迷宫" symptom (none of the seeded docs contain either token).
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "图书馆秘境"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Total, "absent title must return 0, not fuzzy noise")
}

// TestGalgameSearch_DashSubtitleTitle is the end-to-end regression for the
// reported bug: a game whose only name is a "-サブタイトル-" title
// (`CRAZY CHA!N -エルピスの鎖-`, VNDB v54934, a status=2 draft) was unfindable by
// its exact name because Meilisearch parsed `-エルピスの鎖` as a NEGATION term and
// excluded the game itself. sanitizeQuery neutralizes the operator; the verbatim
// title must now resolve to the game — under the real search path (default
// MatchingStrategy=All + score floor + status[0,2] filter).
func TestGalgameSearch_DashSubtitleTitle(t *testing.T) {
	clearIndex(t, IndexGalgames)
	d := newGalgameDoc(51456, "v54934", "CRAZY CHA!N -エルピスの鎖-", "")
	d.Status = 2 // unclaimed VNDB draft, as on prod
	seedGalgames(t, d)

	// The verbatim title (dashes included) must find the game.
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Query:    "CRAZY CHA!N -エルピスの鎖-",
		Statuses: []int{0, 2}, // publish/claim search scope
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.Total, "verbatim dash-subtitle title must resolve to the game")
	assert.EqualValues(t, 51456, resp.Items[0]["id"])

	// A genuine negation intent is intentionally NOT supported here — the '-' is
	// neutralized, so this still finds the game rather than excluding it.
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Query:    "CRAZY -エルピス",
		Statuses: []int{0, 2},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total, "leading-dash must not act as a negation operator")
}

func TestGalgameSearch_ByVNDBID(t *testing.T) {
	clearIndex(t, IndexGalgames)
	seedGalgames(t,
		newGalgameDoc(10, "v17", "ようこそ", "Welcome"),
		newGalgameDoc(11, "v170", "decoy", "decoy"),
	)

	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Query: "v17",
	})
	require.NoError(t, err)
	// v17 should rank above v170 (exact match on vndb_id, which disables typo)
	assert.GreaterOrEqual(t, resp.Total, int64(1))
	assert.EqualValues(t, 10, resp.Items[0]["id"])
}

// TestGalgameSearch_VNDBID_ExactSingle locks in the exact single-galgame
// lookup: a bare "v<digits>" query resolves to the ONE game with that unique
// vndb_id via an exact filter — no prefix bleed (v1965 must not also return
// v19658/v196580 the way full-text would), case-insensitive, empty on no match.
func TestGalgameSearch_VNDBID_ExactSingle(t *testing.T) {
	clearIndex(t, IndexGalgames)
	seedGalgames(t,
		newGalgameDoc(20, "v1965", "マージ", "Marginal"),
		newGalgameDoc(21, "v19658", "永不枯萎", "Never Wither"),
		newGalgameDoc(22, "v196580", "decoy", "decoy"),
	)

	// exact id → exactly one, no prefix bleed to v19658 / v196580
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "v1965"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Items, 1)
	assert.EqualValues(t, 20, resp.Items[0]["id"])

	// case-insensitive
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "V19658"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Items, 1)
	assert.EqualValues(t, 21, resp.Items[0]["id"])

	// no such id → empty result (not an error)
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{Query: "v99999999"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Total)
}

func TestNormalizeVNDBID(t *testing.T) {
	cases := map[string]string{
		"v17": "v17", "V17": "v17", " v17 ": "v17", "v19658": "v19658",
		"v": "", "17": "", "v17a": "", "abc": "", "": "", "v 17": "", "vv1": "",
	}
	for in, want := range cases {
		assert.Equal(t, want, normalizeVNDBID(in), "in=%q", in)
	}
}

func TestGalgameSearch_StatusFilter(t *testing.T) {
	clearIndex(t, IndexGalgames)
	d1 := newGalgameDoc(20, "v20", "pub1", "Published One")
	d1.Status = 0
	d2 := newGalgameDoc(21, "v21", "draft1", "Draft One")
	d2.Status = 2
	d3 := newGalgameDoc(22, "v22", "banned1", "Banned One")
	d3.Status = 1
	seedGalgames(t, d1, d2, d3)

	// Filter by published only
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Statuses: []int{0},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)

	// Filter: draft + banned
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Statuses: []int{1, 2},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)

	// No filter: all 3
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), resp.Total)
}

func TestGalgameSearch_TagIDs_AND(t *testing.T) {
	clearIndex(t, IndexGalgames)
	d1 := newGalgameDoc(30, "v30", "a", "A")
	d1.TagIDs = []int{1, 2, 3}
	d2 := newGalgameDoc(31, "v31", "b", "B")
	d2.TagIDs = []int{1, 2}
	d3 := newGalgameDoc(32, "v32", "c", "C")
	d3.TagIDs = []int{1}
	seedGalgames(t, d1, d2, d3)

	// Tags 1 AND 2 → d1, d2
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		TagIDs: []int{1, 2},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), resp.Total)

	// Tags 1 AND 2 AND 3 → only d1
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		TagIDs: []int{1, 2, 3},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
}

func TestGalgameSearch_ReleasedYearRange(t *testing.T) {
	clearIndex(t, IndexGalgames)
	y2018, y2020, y2022 := 2018, 2020, 2022
	d1 := newGalgameDoc(40, "v40", "old", "old")
	d1.ReleasedYear = &y2018
	ts2018 := time.Date(2018, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	d1.ReleasedTS = &ts2018
	d2 := newGalgameDoc(41, "v41", "mid", "mid")
	d2.ReleasedYear = &y2020
	ts2020 := time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	d2.ReleasedTS = &ts2020
	d3 := newGalgameDoc(42, "v42", "new", "new")
	d3.ReleasedYear = &y2022
	ts2022 := time.Date(2022, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	d3.ReleasedTS = &ts2022
	seedGalgames(t, d1, d2, d3)

	// "2019" → Jan 1 2019, "2021" → Dec 31 2021 23:59:59 — only d2 (2020) falls in range.
	from := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2021, 12, 31, 23, 59, 59, 0, time.UTC).Unix()
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		ReleasedFromTS: from,
		ReleasedToTS:   to,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.EqualValues(t, 41, resp.Items[0]["id"])
}

func TestGalgameSearch_Facets(t *testing.T) {
	clearIndex(t, IndexGalgames)
	d1 := newGalgameDoc(50, "v50", "a", "A")
	d1.AgeLimit = "all"
	d1.OriginalLanguage = "ja-jp"
	d2 := newGalgameDoc(51, "v51", "b", "B")
	d2.AgeLimit = "r18"
	d2.OriginalLanguage = "ja-jp"
	d3 := newGalgameDoc(52, "v52", "c", "C")
	d3.AgeLimit = "r18"
	d3.OriginalLanguage = "en-us"
	seedGalgames(t, d1, d2, d3)

	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		WantFacets: true,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Facets)
	assert.Equal(t, 1, resp.Facets["age_limit"]["all"])
	assert.Equal(t, 2, resp.Facets["age_limit"]["r18"])
	assert.Equal(t, 2, resp.Facets["original_language"]["ja-jp"])
	assert.Equal(t, 1, resp.Facets["original_language"]["en-us"])
}

func TestGalgameSearch_IncludeIntro(t *testing.T) {
	clearIndex(t, IndexGalgames)
	d1 := newGalgameDoc(60, "v60", "游戏一", "Game One")
	d1.IntroZhCN = "这是一个关于魔法少女的故事"
	d2 := newGalgameDoc(61, "v61", "游戏二", "Game Two")
	d2.IntroZhCN = "普通的校园日常"
	seedGalgames(t, d1, d2)

	// WITHOUT include_intro: "魔法" doesn't match any name field → 0 hits
	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Query: "魔法",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(0), resp.Total)

	// WITH include_intro: finds the one whose intro mentions 魔法
	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Query:        "魔法",
		IncludeIntro: true,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.EqualValues(t, 60, resp.Items[0]["id"])
}

func TestGalgameSearch_Sort(t *testing.T) {
	clearIndex(t, IndexGalgames)
	ts1 := int64(1_600_000_000)
	ts2 := int64(1_700_000_000)
	ts3 := int64(1_800_000_000)
	d1 := newGalgameDoc(70, "v70", "a", "A")
	d1.ReleasedTS = &ts1
	d2 := newGalgameDoc(71, "v71", "b", "B")
	d2.ReleasedTS = &ts2
	d3 := newGalgameDoc(72, "v72", "c", "C")
	d3.ReleasedTS = &ts3
	seedGalgames(t, d1, d2, d3)

	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Sort: "released_desc",
	})
	require.NoError(t, err)
	require.Len(t, resp.Items, 3)
	assert.EqualValues(t, 72, resp.Items[0]["id"])
	assert.EqualValues(t, 71, resp.Items[1]["id"])
	assert.EqualValues(t, 70, resp.Items[2]["id"])
}

func TestGalgameSearch_Pagination(t *testing.T) {
	clearIndex(t, IndexGalgames)
	docs := make([]*GalgameDoc, 0, 30)
	for i := 0; i < 30; i++ {
		docs = append(docs, newGalgameDoc(100+i, fmt.Sprintf("v%d", 100+i), fmt.Sprintf("g%d", i), fmt.Sprintf("G%d", i)))
	}
	seedGalgames(t, docs...)

	resp, err := testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Page:  1,
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(30), resp.Total)
	assert.Len(t, resp.Items, 10)

	resp, err = testSvc.SearchGalgames(context.Background(), &GalgameSearchRequest{
		Page:  3,
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 10)
}

// ═══════════════════════════════════════════════════════════════════
// Tag search
// ═══════════════════════════════════════════════════════════════════

func seedTags(t *testing.T, docs ...*TagDoc) {
	t.Helper()
	_, err := testClient.Index(IndexTags).AddDocuments(docs, nil)
	require.NoError(t, err)
	waitForTasks(t, IndexTags)
}

func TestTagSearch_ByName(t *testing.T) {
	clearIndex(t, IndexTags)
	seedTags(t,
		&TagDoc{ID: 1, Name: "校园", Category: "content", GalgameCount: 100, Aliases: []string{}},
		&TagDoc{ID: 2, Name: "战斗", Category: "content", GalgameCount: 50, Aliases: []string{}},
		&TagDoc{ID: 3, Name: "学园祭", Aliases: []string{"文化祭"}, Category: "content", GalgameCount: 30},
	)

	resp, err := testSvc.SearchTags(context.Background(), &TagSearchRequest{Query: "校园"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(resp.Items), 1)
	assert.Equal(t, "校园", resp.Items[0].Name)
}

func TestTagSearch_ByAlias(t *testing.T) {
	clearIndex(t, IndexTags)
	seedTags(t,
		&TagDoc{ID: 10, Name: "Role Playing Game", Aliases: []string{"RPG"}, Category: "content"},
	)

	resp, err := testSvc.SearchTags(context.Background(), &TagSearchRequest{Query: "RPG"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Equal(t, "Role Playing Game", resp.Items[0].Name)
}

func TestTagSearch_CategoryFilter(t *testing.T) {
	clearIndex(t, IndexTags)
	seedTags(t,
		&TagDoc{ID: 100, Name: "AAA", Category: "content", Aliases: []string{}},
		&TagDoc{ID: 101, Name: "AAA ero", Category: "sexual", Aliases: []string{}},
	)

	resp, err := testSvc.SearchTags(context.Background(), &TagSearchRequest{
		Query:    "AAA",
		Category: "content",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Equal(t, "content", resp.Items[0].Category)
}

func TestTagSearch_EmptyQueryReturnsTopByCount(t *testing.T) {
	clearIndex(t, IndexTags)
	seedTags(t,
		&TagDoc{ID: 200, Name: "low", GalgameCount: 1, Aliases: []string{}},
		&TagDoc{ID: 201, Name: "mid", GalgameCount: 100, Aliases: []string{}},
		&TagDoc{ID: 202, Name: "high", GalgameCount: 999, Aliases: []string{}},
	)

	resp, err := testSvc.SearchTags(context.Background(), &TagSearchRequest{Limit: 3})
	require.NoError(t, err)
	require.Len(t, resp.Items, 3)
	assert.Equal(t, "high", resp.Items[0].Name)
	assert.Equal(t, "mid", resp.Items[1].Name)
	assert.Equal(t, "low", resp.Items[2].Name)
}

// ═══════════════════════════════════════════════════════════════════
// Official search
// ═══════════════════════════════════════════════════════════════════

func seedOfficials(t *testing.T, docs ...*OfficialDoc) {
	t.Helper()
	_, err := testClient.Index(IndexOfficials).AddDocuments(docs, nil)
	require.NoError(t, err)
	waitForTasks(t, IndexOfficials)
}

func TestOfficialSearch_ByName(t *testing.T) {
	clearIndex(t, IndexOfficials)
	seedOfficials(t,
		&OfficialDoc{ID: 1, Name: "Key", Category: "company", Lang: "ja", Aliases: []string{}},
		&OfficialDoc{ID: 2, Name: "Visual Arts", Original: "ビジュアルアーツ", Category: "company", Lang: "ja", Aliases: []string{}},
	)

	resp, err := testSvc.SearchOfficials(context.Background(), &OfficialSearchRequest{Query: "Key"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Equal(t, "Key", resp.Items[0].Name)
}

func TestOfficialSearch_ByOriginal(t *testing.T) {
	clearIndex(t, IndexOfficials)
	seedOfficials(t,
		&OfficialDoc{ID: 10, Name: "Tyrell PICCOLO", Original: "たいれるPICCOLO", Category: "company", Aliases: []string{}},
	)

	resp, err := testSvc.SearchOfficials(context.Background(), &OfficialSearchRequest{Query: "たいれる"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
}

func TestOfficialSearch_LangFilter(t *testing.T) {
	clearIndex(t, IndexOfficials)
	seedOfficials(t,
		&OfficialDoc{ID: 20, Name: "JA Studio", Category: "company", Lang: "ja", Aliases: []string{}},
		&OfficialDoc{ID: 21, Name: "EN Studio", Category: "company", Lang: "en", Aliases: []string{}},
	)

	resp, err := testSvc.SearchOfficials(context.Background(), &OfficialSearchRequest{
		Query: "Studio",
		Lang:  "ja",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Total)
	assert.Equal(t, "ja", resp.Items[0].Lang)
}

// ═══════════════════════════════════════════════════════════════════
// Settings
// ═══════════════════════════════════════════════════════════════════

func TestEnsureIndexes_Idempotent(t *testing.T) {
	// Already called in TestMain — call again, verify no error
	err := EnsureIndexes(testClient)
	require.NoError(t, err)

	// Settings should be applied
	settings, err := testClient.Index(IndexGalgames).GetSettings()
	require.NoError(t, err)
	assert.Contains(t, settings.SearchableAttributes, "vndb_id")
	assert.Contains(t, settings.FilterableAttributes, "tag_ids")
	assert.Contains(t, settings.SortableAttributes, "view")
}
