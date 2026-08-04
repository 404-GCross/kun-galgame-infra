// public_intro_search_test.go — A2-1f: the works index's synopsis text and the
// `search_intro=` opt-in, plus the tag-level `sexual` flag.
//
// The load-bearing case is the DEFAULT one: the index now CONTAINS intro
// fields, so a caller who does not ask for them must still match nothing in
// them. That is not a property of the index (its searchable list includes
// them) — it is a property of the per-request attributesToSearchOn restriction,
// which is exactly the kind of thing that silently rots. Hence a synopsis-ONLY
// term: a word that appears in no title anywhere in the corpus, so the default
// lane can only find it if the restriction is gone.
package service

import (
	"os"
	"strings"
	"testing"

	infrasearch "api/internal/infrastructure/search"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
	"api/pkg/config"
)

// worksSearchClient opens a Meilisearch client under this package's own index
// prefix, so a settings assertion reads the very index worksSearchIndexer
// configured.
func worksSearchClient(t *testing.T) *infrasearch.Client {
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
	return client
}

// introOnlyTerm appears in ONE work's synopsis and in no title in the corpus.
const introOnlyTerm = "夏至祭"

// seedIntroCorpus indexes three works: one with the synopsis-only term, one
// whose TITLE carries a term that also appears in the first one's synopsis (so
// the ranking claim can be checked), and one plain control.
func seedIntroCorpus(t *testing.T) (*PublicService, map[string]int64) {
	t.Helper()
	cleanTables(t)
	idx := worksSearchIndexer(t)
	svc := newPublicSvc().WithWorksSearch(idx)

	withIntro := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "陽だまりの詩")
	titled := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "灯火の記憶")
	plain := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "無縁の物語")

	indexWorks(t, idx, []catsearch.WorkDocInput{
		{
			ID: withIntro.ID, DisplayName: "陽だまりの詩", OLang: "ja",
			ContentRating: model.ContentRatingAllAges, UpdatedTS: 1700000001, Popularity: 1,
			SourceKeys: []string{"vndb"},
			Intros: []catsearch.WorkDocIntro{
				// Carries BOTH the synopsis-only term and the other work's title
				// word, so "灯火" must still rank the titled work first.
				{Lang: "ja", Text: "主人公は" + introOnlyTerm + "の夜に灯火を見つける物語。"},
				{Lang: "zh-Hans", Text: "主角在祭典之夜找到灯火的故事。"},
			},
		},
		{
			ID: titled.ID, DisplayName: "灯火の記憶", OLang: "ja",
			ContentRating: model.ContentRatingAllAges, UpdatedTS: 1700000002, Popularity: 1,
			SourceKeys: []string{"vndb"},
		},
		{
			ID: plain.ID, DisplayName: "無縁の物語", OLang: "ja",
			ContentRating: model.ContentRatingAllAges, UpdatedTS: 1700000003, Popularity: 1,
			SourceKeys: []string{"vndb"},
		},
	})
	return svc, map[string]int64{"intro": withIntro.ID, "titled": titled.ID, "plain": plain.ID}
}

// TestWorksSearchIntroIsOptIn is the byte-freeze gate: a term that exists ONLY
// in a synopsis is unfindable by default and findable with search_intro=1.
func TestWorksSearchIntroIsOptIn(t *testing.T) {
	svc, ids := seedIntroCorpus(t)

	if got := searchIDs(t, svc, WorksSearchFilter{Q: introOnlyTerm}); len(got) != 0 {
		t.Fatalf("default search matched the synopsis-only term: %v — attributesToSearchOn restriction is gone", got)
	}
	got := searchIDs(t, svc, WorksSearchFilter{Q: introOnlyTerm, SearchIntro: true})
	if len(got) != 1 || got[0] != ids["intro"] {
		t.Fatalf("search_intro=1 got %v, want exactly [%d]", got, ids["intro"])
	}

	// Titles keep working identically on BOTH lanes — widening must add, never
	// move, results.
	for _, si := range []bool{false, true} {
		got := searchIDs(t, svc, WorksSearchFilter{Q: "陽だまり", SearchIntro: si})
		if len(got) != 1 || got[0] != ids["intro"] {
			t.Fatalf("title search (search_intro=%v) got %v, want [%d]", si, got, ids["intro"])
		}
	}
}

// TestWorksSearchIntroNeverOutranksATitle pins the attribute ordering: the
// intro fields sit last in the searchable list, so a work whose TITLE carries
// the term must precede one that merely mentions it in its synopsis.
func TestWorksSearchIntroNeverOutranksATitle(t *testing.T) {
	svc, ids := seedIntroCorpus(t)

	got := searchIDs(t, svc, WorksSearchFilter{Q: "灯火", SearchIntro: true})
	if len(got) != 2 {
		t.Fatalf("got %v, want both the titled work and the synopsis mention", got)
	}
	if got[0] != ids["titled"] {
		t.Fatalf("ranked %v first; a title match must precede a synopsis match (want %d)", got[0], ids["titled"])
	}
}

// TestWorksSearchIntroIsLanguageBucketed: a Chinese synopsis is matched by a
// Chinese query even though the work's own language is Japanese — the field is
// bucketed so cmn tokenization applies to it.
func TestWorksSearchIntroIsLanguageBucketed(t *testing.T) {
	svc, ids := seedIntroCorpus(t)

	got := searchIDs(t, svc, WorksSearchFilter{Q: "祭典之夜", SearchIntro: true})
	if len(got) != 1 || got[0] != ids["intro"] {
		t.Fatalf("zh synopsis search got %v, want [%d]", got, ids["intro"])
	}
}

// TestWorkDocIntroBucketingAndTruncation is the pure projection case (no
// database, no Meilisearch): buckets by language, joins co-bucketed languages,
// caps on a RUNE boundary.
func TestWorkDocIntroBucketingAndTruncation(t *testing.T) {
	long := strings.Repeat("あ", catsearch.IntroMaxRunes+500)
	doc := catsearch.BuildWorkDoc(catsearch.WorkDocInput{
		ID: 1, DisplayName: "Doc", OLang: "ja",
		Intros: []catsearch.WorkDocIntro{
			{Lang: "ja", Text: long},
			{Lang: "zh-Hans", Text: "简体"},
			{Lang: "zh-Hant", Text: "繁體"},
			{Lang: "en", Text: "english"},
			{Lang: "ja", Text: "  "}, // blank rows never enter a bucket
		},
	})

	if n := len([]rune(doc.IntroJa)); n != catsearch.IntroMaxRunes {
		t.Fatalf("ja intro = %d runes, want the %d cap", n, catsearch.IntroMaxRunes)
	}
	if !utf8Valid(doc.IntroJa) {
		t.Fatalf("truncation cut mid-rune — the document would be invalid UTF-8")
	}
	// zh-Hans and zh-Hant share the zh bucket and are BOTH searchable.
	if doc.IntroZh != "简体\n繁體" {
		t.Fatalf("zh bucket = %q, want both variants joined", doc.IntroZh)
	}
	if doc.IntroOther != "english" {
		t.Fatalf("other bucket = %q", doc.IntroOther)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestEnsureIndexesConvergesOnSecondRun pins the A2-1d discipline this wave
// rides: EnsureIndexes is the ONLY carrier of a settings change, and running it
// twice must be a no-op — the settings it reads back are the ones it declared.
func TestEnsureIndexesConvergesOnSecondRun(t *testing.T) {
	idx := worksSearchIndexer(t) // already ran EnsureIndexes once
	_ = idx

	client := worksSearchClient(t)
	if err := catsearch.EnsureIndexes(client); err != nil {
		t.Fatalf("second EnsureIndexes: %v", err)
	}
	got, err := client.Index(catsearch.IndexWorks).GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	want := append(append([]string{}, catsearch.WorksTitleSearchable...),
		"intro_zh", "intro_ja", "intro_other")
	if len(got.SearchableAttributes) != len(want) {
		t.Fatalf("searchable = %v, want %v", got.SearchableAttributes, want)
	}
	for i := range want {
		if got.SearchableAttributes[i] != want[i] {
			t.Fatalf("searchable[%d] = %q, want %q (titles must precede intros)",
				i, got.SearchableAttributes[i], want[i])
		}
	}
}

// ── tag-level sexual (A2-1f) ────────────────────────────────────────────────

// TestTagSexualReachesBothFacesFromTheColumn covers the A2-1f tag axis on BOTH
// faces: the browse row and the record agree, a sexual-category tag flags true, a
// plain one false, and a tag nobody ever flagged (pure folksonomy) is false — the
// "no axis" case the Tier-A caveat is about.
//
// The flag is catalog_tag.sexual since the W1-pre nativization (refs/proj/140): the
// read-time derivation through the A2-0 identity anchors into galgame_tag.category
// moved into wikirescue step r, which writes THAT SAME derivation onto this column
// — anchor discipline included, and pinned there (TestTagMirror,
// TestTagVocabularySexualIgnoresNonExactAndForeignAnchors). What this suite owes is
// the other half: that whatever the column says reaches both faces intact.
func TestTagSexualReachesBothFacesFromTheColumn(t *testing.T) {
	cleanTables(t)
	cleanTagTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	sexualTag := createCanonicalTag(t, "エロ(a2-1f)", model.TagTierCore, model.TagKindContent)
	contentTag := createCanonicalTag(t, "純愛(a2-1f)", model.TagTierCore, model.TagKindContent)
	techTag := createCanonicalTag(t, "実写(a2-1f)", model.TagTierCore, model.TagKindMeta)
	// Never flagged: a canonical tag that exists only through folksonomy, whose
	// sources publish no safety axis at all.
	folkTag := createCanonicalTag(t, "百合(a2-1f)", model.TagTierCore, model.TagKindContent)
	if err := testDB.Exec(
		`UPDATE catalog_tag SET sexual = true WHERE id = ?`, sexualTag).Error; err != nil {
		t.Fatalf("flag the sexual tag: %v", err)
	}

	want := map[int64]bool{
		sexualTag: true, contentTag: false, techTag: false, folkTag: false,
	}

	// (1) the browse lane.
	list, err := svc.TagsList(ctx, TagsListFilter{}, "", 50)
	if err != nil {
		t.Fatalf("TagsList: %v", err)
	}
	seen := 0
	for _, it := range list.Items {
		exp, ok := want[it.ID]
		if !ok {
			continue
		}
		if it.Sexual != exp {
			t.Fatalf("list tag %d (%s) sexual=%v, want %v", it.ID, it.Name, it.Sexual, exp)
		}
		seen++
	}
	if seen != len(want) {
		t.Fatalf("browse lane covered %d fixture tags, want %d", seen, len(want))
	}

	// (2) the record — must agree with its own list row.
	for id, exp := range want {
		rec, found, err := svc.TagDetail(ctx, id, false, false, 50, 0)
		if err != nil || !found {
			t.Fatalf("TagDetail %d: found=%v err=%v", id, found, err)
		}
		if rec.Sexual != exp {
			t.Fatalf("detail tag %d sexual=%v, want %v", id, rec.Sexual, exp)
		}
	}
}
