// public_works_search_test.go — A2-1d: the works product search face.
//
// Two layers, deliberately:
//
//   - PURE cases (no Meilisearch, no database) pin the contract surface that
//     must hold whatever the engine does: the compiled filter expression, the
//     closed sort/facet vocabularies, the facet re-keying and the v\d+
//     detector.
//   - END-TO-END cases index real documents — built with the SAME
//     search.BuildWorkDoc production uses, so nothing here can pass against a
//     look-alike the reindexer would never emit — and assert the wave's load-
//     bearing invariant: total, the facet distribution and items are three
//     views of ONE filtered set.
//
// The end-to-end layer needs Meilisearch and skips loudly when it is not
// reachable. It runs under its OWN index prefix (test_svc_) so it can never
// collide with the search package's own suite, let alone a dev index.
package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	infrasearch "api/internal/infrastructure/search"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
	"api/pkg/config"
)

// ─────────────────────────── pure contract cases ───────────────────────────

// TestWorksSearchFilterCompilation pins the one-gate rule at its source: every
// caller-visible filter must appear in the Meilisearch expression. A filter
// that silently fell out here would be applied (if at all) during the DB
// re-hydration — the deprecated face's content_limit trap, where total counted
// rows the caller could never receive.
func TestWorksSearchFilterCompilation(t *testing.T) {
	claimed, r18 := true, model.ContentRatingR18
	f := WorksSearchFilter{
		ContentRating: &r18, Claimed: &claimed,
		TagIDs: []int64{7}, LabelID: 8, EngineID: 9, SeriesID: 10,
		ReleasedAfter: 20200101, ReleasedBefore: 20241231,
		NSFW: true,
	}
	got := f.meiliFilter("")
	for _, want := range []string{
		"content_rating = 2", "claimed = true",
		"tag_ids = 7", "label_ids = 8", "engine_ids = 9", "series_ids = 10",
		"released_ord >= 20200101", "released_ord <= 20241231",
		"(olang = 'ja' OR olang STARTS WITH 'zh')",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("filter %q missing clause %q", got, want)
		}
	}
	// nsfw=true means NO exclusion clause; the r18 rows are what was asked for.
	if strings.Contains(got, "content_rating != 2") {
		t.Fatalf("nsfw=true must not exclude r18: %q", got)
	}

	// The sfw default is the opposite: the exclusion rides in the expression,
	// so it constrains total and the facets too, not just the page.
	if sfw := (WorksSearchFilter{}).meiliFilter(""); !strings.Contains(sfw, "content_rating != 2") {
		t.Fatalf("sfw filter %q must exclude r18 inside Meilisearch", sfw)
	}

	// olang=all removes the gate entirely; an explicit set becomes an IN list
	// with every value quoted and escaped.
	if all := (WorksSearchFilter{OLang: PublicOLang{All: true}}).meiliFilter(""); strings.Contains(all, "olang") {
		t.Fatalf("olang=all must emit no olang clause: %q", all)
	}
	explicit := (WorksSearchFilter{OLang: PublicOLang{Values: []string{"en", "ko'x"}}}).meiliFilter("")
	if !strings.Contains(explicit, `olang IN ['en', 'ko\'x']`) {
		t.Fatalf("explicit olang clause = %q", explicit)
	}

	// The v-shortcut pins one document and keeps every other clause.
	pinned := (WorksSearchFilter{TagIDs: []int64{3}}).meiliFilter("w42")
	if !strings.Contains(pinned, "id = 'w42'") || !strings.Contains(pinned, "tag_ids = 3") {
		t.Fatalf("pinned filter = %q, want the doc id AND the caller's filters", pinned)
	}

	// The zero value still gates: an empty expression would serve r18 to an
	// anonymous caller.
	if (WorksSearchFilter{}).meiliFilter("") == "" {
		t.Fatal("the default filter must never be empty")
	}
}

// TestWorksSearchClosedVocabularies pins the two token sets the handler 400s
// on, and that every advertised token actually resolves.
func TestWorksSearchClosedVocabularies(t *testing.T) {
	for _, tok := range WorksSearchSortTokens {
		if _, ok := WorksSearchSortRule(tok); !ok {
			t.Fatalf("advertised sort token %q does not resolve", tok)
		}
	}
	if rule, _ := WorksSearchSortRule(""); rule != "" {
		t.Fatalf("empty sort must mean relevance, got %q", rule)
	}
	if rule, _ := WorksSearchSortRule("relevance"); rule != "" {
		t.Fatalf("relevance must emit no sort, got %q", rule)
	}
	for tok, want := range map[string]string{
		"released_desc": "released_ord:desc",
		"released_asc":  "released_ord:asc",
		"updated":       "updated_ts:desc",
		"popularity":    "popularity:desc",
	} {
		if rule, _ := WorksSearchSortRule(tok); rule != want {
			t.Fatalf("sort %q = %q, want %q", tok, rule, want)
		}
	}
	// `view` was the deprecated face's fifth lane (the wiki's page-view
	// counter). The registry has no counterpart, so it must NOT quietly work.
	if _, ok := WorksSearchSortRule("view"); ok {
		t.Fatal("sort=view must be rejected — popularity replaced it")
	}
	if _, ok := WorksSearchSortRule("nonsense"); ok {
		t.Fatal("an unknown sort token must be rejected, not ignored")
	}

	for _, tok := range WorksSearchFacetTokens {
		if !IsWorksSearchFacet(tok) {
			t.Fatalf("advertised facet token %q is not accepted", tok)
		}
	}
	// The facet vocabulary speaks FILTER PARAMETER names, never the index's own
	// attribute names — those must not leak into the contract.
	for _, leaked := range []string{"tag_ids", "label_ids", "source_keys", "released_ord", "popularity"} {
		if IsWorksSearchFacet(leaked) {
			t.Fatalf("index attribute %q must not be a public facet token", leaked)
		}
	}

	// Requested tokens map onto index attributes in order, deduplicated.
	got := worksSearchMeiliFacets([]string{"tag_id", "content_rating", "tag_id"})
	if len(got) != 2 || got[0] != "tag_ids" || got[1] != "content_rating" {
		t.Fatalf("facet attrs = %v, want [tag_ids content_rating]", got)
	}
}

// TestWorksSearchFacetProjection pins the re-keying: outer keys become the
// filter parameter the caller would pass back, and content_rating counts are
// keyed by the public strings — never the enum ints Meilisearch stores.
func TestWorksSearchFacetProjection(t *testing.T) {
	dist := map[string]map[string]int64{
		"content_rating": {"0": 10, "2": 3},
		"tag_ids":        {"41": 5},
		"olang":          {"ja": 12},
	}
	out := projectWorksSearchFacets([]string{"content_rating", "tag_id", "olang"}, dist)
	if got := out["content_rating"]["all_ages"]; got != 10 {
		t.Fatalf("content_rating.all_ages = %d, want 10 (from raw key \"0\")", got)
	}
	if got := out["content_rating"]["r18"]; got != 3 {
		t.Fatalf("content_rating.r18 = %d, want 3", got)
	}
	if _, leaked := out["content_rating"]["0"]; leaked {
		t.Fatal("the raw enum key must not survive the projection")
	}
	if out["tag_id"]["41"] != 5 {
		t.Fatalf("tag_id distribution lost: %+v", out["tag_id"])
	}
	if _, leaked := out["tag_ids"]; leaked {
		t.Fatal("the index attribute name must not appear as a wire key")
	}
	if out["olang"]["ja"] != 12 {
		t.Fatalf("olang distribution lost: %+v", out["olang"])
	}
	// No facets asked for → the block is absent, not an empty object.
	if projectWorksSearchFacets(nil, dist) != nil {
		t.Fatal("facets must be nil when none were requested")
	}
}

// TestNormalizeVNDBID pins the short-circuit detector: only an EXACT v<digits>
// query delegates to the anchor lookup; anything else stays full text.
func TestNormalizeVNDBID(t *testing.T) {
	for in, want := range map[string]string{
		"v19658":  "v19658",
		"V19658":  "v19658",
		" v123 ":  "v123",
		"v1":      "v1",
		"v":       "",
		"v19658a": "",
		"vndb":    "",
		"19658":   "",
		"":        "",
		"v 19658": "",
		"ヴァルキリー":  "",
	} {
		if got := normalizeVNDBID(in); got != want {
			t.Fatalf("normalizeVNDBID(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─────────────────────────── end-to-end cases ───────────────────────────

const worksSearchTestPrefix = "test_svc_"

// worksSearchIndexer connects to Meilisearch under this package's OWN index
// prefix. It skips (never fails) when Meilisearch is unreachable, and always
// tears its index down.
func worksSearchIndexer(t *testing.T) *catsearch.Indexer {
	t.Helper()
	// No host, no test. NewClient never dials — it only builds a handle — so a
	// hard-coded 127.0.0.1:7700 fallback could not be caught by the err check
	// below: without a Meilisearch the test proceeded to a connection-refused
	// FAILURE instead of a skip, and with an unrelated local Meilisearch it
	// failed on a 401. The CI integration job sets this variable; the
	// no-services job deliberately does not.
	host := os.Getenv("MEILISEARCH_TEST_HOST")
	if host == "" {
		t.Skip("MEILISEARCH_TEST_HOST unset — search-backed test not run")
	}
	client, err := infrasearch.NewClient(config.MeilisearchConfig{
		Host: host, APIKey: os.Getenv("MEILISEARCH_TEST_API_KEY"), IndexPrefix: worksSearchTestPrefix,
	})
	if err != nil {
		t.Skipf("meilisearch client: %v", err)
	}
	if err := client.Health(); err != nil {
		t.Skipf("meilisearch unreachable: %v", err)
	}
	if err := catsearch.EnsureIndexes(client); err != nil {
		t.Fatalf("ensure indexes: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.Svc().DeleteIndex(client.IndexUID(catsearch.IndexWorks))
	})
	// A fresh corpus per test: delete every document, then push.
	task, err := client.Index(catsearch.IndexWorks).DeleteAllDocuments(nil)
	if err != nil {
		t.Fatalf("clear works index: %v", err)
	}
	if _, err := client.Svc().WaitForTask(task.TaskUID, 20*time.Millisecond); err != nil {
		t.Fatalf("wait clear: %v", err)
	}
	return catsearch.NewIndexer(client)
}

// indexWorks projects the given registry works exactly as cmd/reindex-catalog
// does — same BuildWorkDoc, same facet inputs — and waits for the documents to
// be searchable.
func indexWorks(t *testing.T, idx *catsearch.Indexer, docs []catsearch.WorkDocInput) {
	t.Helper()
	built := make([]catsearch.EntityDoc, len(docs))
	for i, in := range docs {
		built[i] = catsearch.BuildWorkDoc(in)
	}
	if err := idx.UpsertBatch(context.Background(), catsearch.IndexWorks, built); err != nil {
		t.Fatalf("upsert works docs: %v", err)
	}
	waitIndexed(t, idx, len(docs))
}

// waitIndexed blocks until the works index reports the expected document count
// (Meilisearch tasks are asynchronous).
func waitIndexed(t *testing.T, idx *catsearch.Indexer, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		n, err := idx.Count(catsearch.IndexWorks)
		if err == nil && int(n) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("index did not reach %d docs (last err %v)", want, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// searchCorpus seeds a registry + index pair used by several cases: four LIVE
// galgame works spanning both ratings, both claim states, two languages and
// three release dates, plus one tag/label/engine/series attachment each.
type searchCorpus struct {
	safe, r18, claimedWork, enWork int64
	tagID, labelID, engineID       int64
	idx                            *catsearch.Indexer
	svc                            *PublicService
}

func seedSearchCorpus(t *testing.T) searchCorpus {
	t.Helper()
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	idx := worksSearchIndexer(t)

	c := searchCorpus{idx: idx}
	c.svc = newPublicSvc().WithWorksSearch(idx)

	safe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "いろとりどりのセカイ")
	r18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "いろとりどりのハイパー")
	claimedW := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "クレイムド作品")
	claimWork(t, claimedW.ID, "galgame_wiki", 42)
	enWork := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "Sekai Project")
	setOLang(t, enWork.ID, "en")
	c.safe, c.r18, c.claimedWork, c.enWork = safe.ID, r18.ID, claimedW.ID, enWork.ID

	c.tagID = createCanonicalTag(t, "純愛", model.TagTierCore, model.TagKindContent)
	c.labelID = addWorkLabel(t, safe.ID, "Favorite", model.LabelKindGameBrand, model.WorkLabelKindBrand)
	c.engineID = createEngine(t, "KiriKiri")
	attachEngine(t, safe.ID, c.engineID)

	// Releases give the three dated works distinct ordinals; enWork stays
	// undated on purpose (it must sort last in BOTH directions).
	createRelease(t, safe.ID, 2020, 3, 1)
	createRelease(t, r18.ID, 2010, 6, 0)
	createRelease(t, claimedW.ID, 2024, 12, 25)

	indexWorks(t, idx, []catsearch.WorkDocInput{
		{ID: safe.ID, DisplayName: "いろとりどりのセカイ", OLang: "ja",
			ContentRating: model.ContentRatingAllAges, ReleasedOrd: 20200301, UpdatedTS: 1700000001,
			Popularity: 3, TagIDs: []int64{c.tagID}, LabelIDs: []int64{c.labelID},
			EngineIDs: []int64{c.engineID}, SourceKeys: []string{"vndb"}},
		{ID: r18.ID, DisplayName: "いろとりどりのハイパー", OLang: "ja",
			ContentRating: model.ContentRatingR18, ReleasedOrd: 20100600, UpdatedTS: 1700000002,
			Popularity: 5, TagIDs: []int64{c.tagID}, SourceKeys: []string{"vndb"}},
		{ID: claimedW.ID, DisplayName: "クレイムド作品", OLang: "ja", Claimed: true,
			ContentRating: model.ContentRatingAllAges, ReleasedOrd: 20241225, UpdatedTS: 1700000003,
			Popularity: 1, SourceKeys: []string{"bangumi"}},
		{ID: enWork.ID, DisplayName: "Sekai Project", OLang: "en",
			ContentRating: model.ContentRatingAllAges, UpdatedTS: 1700000004,
			Popularity: 9, SourceKeys: []string{"vndb"}},
	})
	return c
}

func searchIDs(t *testing.T, svc *PublicService, f WorksSearchFilter) []int64 {
	t.Helper()
	data, err := svc.WorksSearch(t.Context(), f)
	if err != nil {
		t.Fatalf("WorksSearch %+v: %v", f, err)
	}
	out := make([]int64, len(data.Items))
	for i, it := range data.Items {
		out[i] = it.ID
	}
	return out
}

// TestWorksSearchOneGate is THE case of the wave: total, the facet
// distribution and the rows you can actually page through are the same set.
//
// The deprecated face failed exactly here — its Meilisearch filter omitted
// content_limit while the SQL re-hydration applied it, so an sfw caller saw a
// total that counted r18 games it could never receive and lost rows on every
// page. Both halves are asserted: the sfw and nsfw totals differ, and walking
// every page collects exactly `total` rows.
func TestWorksSearchOneGate(t *testing.T) {
	c := seedSearchCorpus(t)
	ctx := t.Context()

	sfw, err := c.svc.WorksSearch(ctx, WorksSearchFilter{OLang: PublicOLang{All: true}, Facets: []string{"content_rating"}})
	if err != nil {
		t.Fatalf("sfw search: %v", err)
	}
	nsfw, err := c.svc.WorksSearch(ctx, WorksSearchFilter{NSFW: true, OLang: PublicOLang{All: true}, Facets: []string{"content_rating"}})
	if err != nil {
		t.Fatalf("nsfw search: %v", err)
	}
	if sfw.Total != 3 || nsfw.Total != 4 {
		t.Fatalf("totals sfw=%d nsfw=%d, want 3 and 4 — the r18 work must be OUT of the sfw total, not just its page",
			sfw.Total, nsfw.Total)
	}
	// The facet distribution rides the same gate: an sfw caller is never told
	// how many r18 works exist.
	if _, leaked := sfw.Facets["content_rating"]["r18"]; leaked {
		t.Fatalf("sfw facet distribution leaked an r18 bucket: %+v", sfw.Facets)
	}
	if nsfw.Facets["content_rating"]["r18"] != 1 {
		t.Fatalf("nsfw facets = %+v, want one r18", nsfw.Facets)
	}

	// Full pagination collects exactly total rows, with no repeats.
	seen := map[int64]bool{}
	for page := 1; page <= 10; page++ {
		data, err := c.svc.WorksSearch(ctx, WorksSearchFilter{
			OLang: PublicOLang{All: true}, Sort: "popularity", Page: page, Limit: 2,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(data.Items) == 0 {
			break
		}
		for _, it := range data.Items {
			if seen[it.ID] {
				t.Fatalf("work %d served twice across pages", it.ID)
			}
			seen[it.ID] = true
		}
	}
	if int64(len(seen)) != sfw.Total {
		t.Fatalf("walked %d rows but total said %d — total and items disagree", len(seen), sfw.Total)
	}
	for _, id := range []int64{c.safe, c.claimedWork, c.enWork} {
		if !seen[id] {
			t.Fatalf("work %d never appeared while paging the whole set", id)
		}
	}
	if seen[c.r18] {
		t.Fatalf("r18 work %d reached an sfw caller", c.r18)
	}
}

// TestWorksSearchFiltersAreOrthogonalToText pins that a text query and each
// filter compose — the results are the INTERSECTION, not one or the other.
func TestWorksSearchFiltersAreOrthogonalToText(t *testing.T) {
	c := seedSearchCorpus(t)

	// The text alone matches both いろとりどり works (r18 needs nsfw).
	if got := searchIDs(t, c.svc, WorksSearchFilter{Q: "いろとりどり", NSFW: true}); len(got) != 2 {
		t.Fatalf("text-only = %v, want both いろとりどり works", got)
	}
	// Text ∧ tag_id still matches both (both carry the tag)…
	if got := searchIDs(t, c.svc, WorksSearchFilter{Q: "いろとりどり", TagIDs: []int64{c.tagID}, NSFW: true}); len(got) != 2 {
		t.Fatalf("text ∧ tag = %v, want 2", got)
	}
	// …but text ∧ label_id narrows to the one work carrying that label.
	got := searchIDs(t, c.svc, WorksSearchFilter{Q: "いろとりどり", LabelID: c.labelID, NSFW: true})
	if len(got) != 1 || got[0] != c.safe {
		t.Fatalf("text ∧ label = %v, want [%d]", got, c.safe)
	}
	// A filter that matches nothing in the text result set yields nothing —
	// never the unfiltered text result.
	if got := searchIDs(t, c.svc, WorksSearchFilter{Q: "いろとりどり", EngineID: 999999, NSFW: true}); len(got) != 0 {
		t.Fatalf("text ∧ unknown engine = %v, want empty", got)
	}
	// engine_id alone.
	if got := searchIDs(t, c.svc, WorksSearchFilter{EngineID: c.engineID, NSFW: true}); len(got) != 1 || got[0] != c.safe {
		t.Fatalf("engine_id = %v, want [%d]", got, c.safe)
	}
	// claimed splits the population both ways.
	yes, no := true, false
	if got := searchIDs(t, c.svc, WorksSearchFilter{Claimed: &yes, OLang: PublicOLang{All: true}}); len(got) != 1 || got[0] != c.claimedWork {
		t.Fatalf("claimed=true = %v, want [%d]", got, c.claimedWork)
	}
	if got := searchIDs(t, c.svc, WorksSearchFilter{Claimed: &no, OLang: PublicOLang{All: true}}); len(got) != 2 {
		t.Fatalf("claimed=false = %v, want the two bodyless sfw works", got)
	}
	// released_* bounds run over the EARLIEST release ordinal, month precision
	// included (the r18 work's 2010-06 is ordinal 20100600).
	if got := searchIDs(t, c.svc, WorksSearchFilter{ReleasedAfter: 20150101, NSFW: true, OLang: PublicOLang{All: true}}); len(got) != 2 {
		t.Fatalf("released_after=2015 = %v, want the 2020 and 2024 works", got)
	}
	if got := searchIDs(t, c.svc, WorksSearchFilter{ReleasedBefore: 20151231, NSFW: true, OLang: PublicOLang{All: true}}); len(got) != 1 || got[0] != c.r18 {
		t.Fatalf("released_before=2015 = %v, want [%d]", got, c.r18)
	}
	// content_rating is an equality filter on top of the nsfw gate.
	r18 := model.ContentRatingR18
	if got := searchIDs(t, c.svc, WorksSearchFilter{ContentRating: &r18, NSFW: true, OLang: PublicOLang{All: true}}); len(got) != 1 || got[0] != c.r18 {
		t.Fatalf("content_rating=r18 = %v, want [%d]", got, c.r18)
	}
}

// TestWorksSearchOLangGate pins the population default and its escape hatch —
// the calendar's rule, applied to search: ja + zh* by default, `all` to opt out.
func TestWorksSearchOLangGate(t *testing.T) {
	c := seedSearchCorpus(t)

	// Default gate: the English-language work is out.
	got := searchIDs(t, c.svc, WorksSearchFilter{})
	for _, id := range got {
		if id == c.enWork {
			t.Fatalf("olang=en work %d survived the default ja+zh gate", c.enWork)
		}
	}
	if len(got) != 2 {
		t.Fatalf("default gate = %v, want the two sfw ja works", got)
	}
	// olang=all lets it in.
	if got := searchIDs(t, c.svc, WorksSearchFilter{OLang: PublicOLang{All: true}}); len(got) != 3 {
		t.Fatalf("olang=all = %v, want 3 sfw works", got)
	}
	// An explicit value selects exactly it.
	if got := searchIDs(t, c.svc, WorksSearchFilter{OLang: PublicOLang{Values: []string{"en"}}}); len(got) != 1 || got[0] != c.enWork {
		t.Fatalf("olang=en = %v, want [%d]", got, c.enWork)
	}
	// olang is an OPEN vocabulary: an unknown value is an empty result, not an
	// error (the calendar's posture, verbatim).
	data, err := c.svc.WorksSearch(t.Context(), WorksSearchFilter{OLang: PublicOLang{Values: []string{"nope"}}})
	if err != nil {
		t.Fatalf("unknown olang must not error: %v", err)
	}
	if data.Total != 0 || len(data.Items) != 0 {
		t.Fatalf("unknown olang = %d items / total %d, want empty", len(data.Items), data.Total)
	}
}

// TestWorksSearchSortLanes walks all five lanes and pins the undated-work rule:
// a work with no dated release sorts LAST in both released directions (its
// released_ord is absent, not zero — zero would put it first on released_asc).
func TestWorksSearchSortLanes(t *testing.T) {
	c := seedSearchCorpus(t)
	all := PublicOLang{All: true}

	desc := searchIDs(t, c.svc, WorksSearchFilter{Sort: "released_desc", OLang: all})
	if len(desc) != 3 || desc[0] != c.claimedWork || desc[1] != c.safe || desc[2] != c.enWork {
		t.Fatalf("released_desc = %v, want [2024 %d, 2020 %d, undated %d]", desc, c.claimedWork, c.safe, c.enWork)
	}
	asc := searchIDs(t, c.svc, WorksSearchFilter{Sort: "released_asc", OLang: all})
	if len(asc) != 3 || asc[0] != c.safe || asc[1] != c.claimedWork || asc[2] != c.enWork {
		t.Fatalf("released_asc = %v, want [2020 %d, 2024 %d, undated %d LAST]", asc, c.safe, c.claimedWork, c.enWork)
	}
	updated := searchIDs(t, c.svc, WorksSearchFilter{Sort: "updated", OLang: all})
	if len(updated) != 3 || updated[0] != c.enWork {
		t.Fatalf("updated = %v, want the newest-updated work %d first", updated, c.enWork)
	}
	pop := searchIDs(t, c.svc, WorksSearchFilter{Sort: "popularity", OLang: all})
	if len(pop) != 3 || pop[0] != c.enWork || pop[2] != c.claimedWork {
		t.Fatalf("popularity = %v, want [9 %d, 3 %d, 1 %d]", pop, c.enWork, c.safe, c.claimedWork)
	}
	// relevance with an empty query degenerates to popularity (nothing to rank
	// on), which is the documented browse order.
	if rel := searchIDs(t, c.svc, WorksSearchFilter{Sort: "relevance", OLang: all}); len(rel) != 3 || rel[0] != c.enWork {
		t.Fatalf("empty-q relevance = %v, want the popularity order", rel)
	}
	// With text, relevance ranks by the match, not by popularity: the r18 work
	// is more popular but the exact title belongs to the other one.
	rel := searchIDs(t, c.svc, WorksSearchFilter{Q: "いろとりどりのセカイ", NSFW: true, OLang: all})
	if len(rel) == 0 || rel[0] != c.safe {
		t.Fatalf("text relevance = %v, want the exact title %d first", rel, c.safe)
	}
}

// TestWorksSearchVNDBShortCircuit pins 裁定 5: an exact v<digits> query resolves
// through the anchor registry instead of full text, still honours the caller's
// gates, and returns this face's envelope (never a 404) on a miss.
func TestWorksSearchVNDBShortCircuit(t *testing.T) {
	c := seedSearchCorpus(t)
	// Anchor the r18 work to a VNDB id whose digits are a PREFIX of another id,
	// which is exactly what full text would bleed across.
	if err := testDB.Create(&model.CatalogExternalRef{
		SourceID: srcVNDB, ExternalID: "v1965", EntityType: model.EntityTypeWork,
		EntityID: c.r18, LinkKind: model.LinkKindExact,
	}).Error; err != nil {
		t.Fatalf("anchor vndb id: %v", err)
	}

	data, err := c.svc.WorksSearch(t.Context(), WorksSearchFilter{Q: "v1965", NSFW: true, OLang: PublicOLang{All: true}})
	if err != nil {
		t.Fatalf("vndb short-circuit: %v", err)
	}
	if data.Total != 1 || len(data.Items) != 1 || data.Items[0].ID != c.r18 {
		t.Fatalf("v1965 = total %d items %+v, want exactly work %d", data.Total, data.Items, c.r18)
	}
	// Case-insensitive.
	if got := searchIDs(t, c.svc, WorksSearchFilter{Q: "V1965", NSFW: true, OLang: PublicOLang{All: true}}); len(got) != 1 || got[0] != c.r18 {
		t.Fatalf("V1965 = %v, want [%d]", got, c.r18)
	}
	// The caller's gates still apply: sfw must not receive the r18 work.
	if got := searchIDs(t, c.svc, WorksSearchFilter{Q: "v1965", OLang: PublicOLang{All: true}}); len(got) != 0 {
		t.Fatalf("sfw v1965 = %v, want empty (the anchored work is r18)", got)
	}
	// An unresolvable id is an empty envelope, never an error or a 404.
	miss, err := c.svc.WorksSearch(t.Context(), WorksSearchFilter{Q: "v999999", NSFW: true})
	if err != nil {
		t.Fatalf("unresolvable vndb id must not error: %v", err)
	}
	if miss.Total != 0 || len(miss.Items) != 0 || miss.Page != 1 {
		t.Fatalf("v999999 = %+v, want an empty page-1 envelope", miss)
	}
}

// TestWorksSearchItemsAreWorksListRows pins 裁定 4: the wire item is the works
// LIST item, include= blocks and all — never a Meilisearch document.
func TestWorksSearchItemsAreWorksListRows(t *testing.T) {
	c := seedSearchCorpus(t)
	addWorkTitle(t, c.safe, "zh-Hans", "五彩斑斓的世界", 0)

	data, err := c.svc.WorksSearch(t.Context(), WorksSearchFilter{
		Q: "いろとりどりのセカイ", Include: ParseWorksListInclude("names,labels"), NSFW: true,
	})
	if err != nil {
		t.Fatalf("WorksSearch include: %v", err)
	}
	if len(data.Items) == 0 {
		t.Fatal("no items")
	}
	it := data.Items[0]
	if it.ID != c.safe {
		t.Fatalf("first item = %d, want %d", it.ID, c.safe)
	}
	// The base works-list fields are hydrated from Postgres, not the index.
	if it.DisplayName != "いろとりどりのセカイ" || it.ContentRating != "all_ages" || it.OLang != "ja" {
		t.Fatalf("base fields not hydrated from the registry: %+v", it)
	}
	if it.ReleaseDate == nil || *it.ReleaseDate != "2020-03-01" {
		t.Fatalf("release_date = %v, want 2020-03-01", it.ReleaseDate)
	}
	if it.Updated == "" {
		t.Fatal("updated must be present on a search row, like a browse row")
	}
	// include= blocks work here exactly as on the browse lane.
	if it.Names == nil || it.Names.ZhCN != "五彩斑斓的世界" {
		t.Fatalf("include=names block = %+v", it.Names)
	}
	if len(it.Labels) != 1 || it.Labels[0].ID != c.labelID {
		t.Fatalf("include=labels block = %+v", it.Labels)
	}
	// Without include=, the blocks stay absent.
	plain, err := c.svc.WorksSearch(t.Context(), WorksSearchFilter{Q: "いろとりどりのセカイ", NSFW: true})
	if err != nil {
		t.Fatalf("WorksSearch plain: %v", err)
	}
	if plain.Items[0].Names != nil || plain.Items[0].Labels != nil {
		t.Fatalf("blocks leaked without include=: %+v", plain.Items[0])
	}
	// The envelope echoes the window actually served.
	if plain.Page != 1 || plain.Limit != 20 {
		t.Fatalf("envelope page/limit = %d/%d, want 1/20", plain.Page, plain.Limit)
	}
}

// TestWorksSearchSanitizeAndThreshold pins the two relevance rules carried over
// from the deprecated face: a leading '-' must not become Meilisearch negation
// (VNDB titles use the -サブタイトル- convention, and pasting one verbatim used
// to exclude the very game being searched), and a weak match is dropped.
func TestWorksSearchSanitizeAndThreshold(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	cleanTaxonomyTables(t)
	idx := worksSearchIndexer(t)
	svc := newPublicSvc().WithWorksSearch(idx)

	hyphen := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "CRAZY CHA!N -エルピスの鎖-")
	other := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "まったく別の作品")
	indexWorks(t, idx, []catsearch.WorkDocInput{
		{ID: hyphen.ID, DisplayName: "CRAZY CHA!N -エルピスの鎖-", OLang: "ja", UpdatedTS: 1, Popularity: 1},
		{ID: other.ID, DisplayName: "まったく別の作品", OLang: "ja", UpdatedTS: 2, Popularity: 2},
	})

	// The exact title, pasted verbatim with its hyphens, must find itself.
	got := searchIDs(t, svc, WorksSearchFilter{Q: "CRAZY CHA!N -エルピスの鎖-"})
	if len(got) == 0 || got[0] != hyphen.ID {
		t.Fatalf("verbatim hyphenated title = %v, want [%d] — '-' was parsed as negation", got, hyphen.ID)
	}
	// A query with no real match returns nothing rather than a weak long tail.
	if got := searchIDs(t, svc, WorksSearchFilter{Q: "存在しない題名ですよこれは"}); len(got) != 0 {
		t.Fatalf("nonsense query = %v, want empty (relevance floor)", got)
	}
}

// TestWorksSearchUnavailableWithoutIndexer pins the misconfiguration path: a
// face with no indexer errors loudly instead of answering "nothing matched".
func TestWorksSearchUnavailableWithoutIndexer(t *testing.T) {
	if _, err := newPublicSvc().WorksSearch(context.Background(), WorksSearchFilter{}); err != ErrSearchUnavailable {
		t.Fatalf("err = %v, want ErrSearchUnavailable", err)
	}
}
