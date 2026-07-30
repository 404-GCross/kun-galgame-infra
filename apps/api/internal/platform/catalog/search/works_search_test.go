package search

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustJSON serializes a document the way it reaches Meilisearch, so a test can
// assert on FIELD PRESENCE (omitempty) and not merely on Go values.
func mustJSON(t *testing.T, d EntityDoc) string {
	t.Helper()
	b, err := json.Marshal(d)
	require.NoError(t, err)
	return string(b)
}

// TestBuildWorkDoc pins the projection the product search's whole filter set
// rests on. No Meilisearch needed — this is the contract between the reindexer
// and the index settings.
func TestBuildWorkDoc(t *testing.T) {
	d := BuildWorkDoc(WorkDocInput{
		ID: 42, DisplayName: "いろとりどりのセカイ", OLang: "zh-Hans",
		ContentRating: 2, Claimed: true, ClaimState: "live",
		ReleasedOrd: 20240600, UpdatedTS: 1700000000, Popularity: 3.5,
		Titles: []WorkDocTitle{
			{Lang: "ja", Title: "いろとりどりのセカイ"}, // == display_name, must not duplicate
			{Lang: "zh", Title: "五彩斑斓的世界", Latin: "Irotoridori no Sekai"},
			{Lang: "", Title: "别名テスト"}, // no lang → guessed
		},
		TagIDs: []int64{7, 9}, LabelIDs: []int64{3}, EngineIDs: []int64{1}, SeriesIDs: []int64{5},
		Sources: []string{"vndb:v19658"}, SourceKeys: []string{"vndb"},
	})

	assert.Equal(t, "w42", d.ID)
	assert.Equal(t, "work", d.EntityType)
	assert.Equal(t, "いろとりどりのセカイ", d.NameJa, "display_name claims its bucket first")
	assert.Equal(t, "五彩斑斓的世界", d.NameZh)
	assert.Equal(t, "Irotoridori no Sekai", d.Latin, "first non-empty latin wins")
	// The ja bucket is taken, so the lang-less third title (guessed ja) becomes
	// an alias — and the title identical to display_name is NOT duplicated
	// there, which is the whole point of the seen-set.
	assert.Equal(t, []string{"别名テスト"}, d.AliasesJa)

	require.NotNil(t, d.ContentRating)
	assert.EqualValues(t, 2, *d.ContentRating)
	require.NotNil(t, d.Claimed)
	assert.True(t, *d.Claimed)
	assert.Equal(t, "zh-Hans", d.OLang, "olang is the registry value verbatim, never re-derived from the title")
	assert.Equal(t, []int64{7, 9}, d.TagIDs)
	assert.Equal(t, []int64{3}, d.LabelIDs)
	assert.Equal(t, []int64{1}, d.EngineIDs)
	assert.Equal(t, []int64{5}, d.SeriesIDs)
	assert.EqualValues(t, 20240600, d.ReleasedOrd)
	assert.EqualValues(t, 1700000000, d.UpdatedTS)

	// An UNDATED work must leave released_ord OFF the document (not 0), so
	// Meilisearch sorts it last in both directions and no released_* bound
	// matches it — the works list's `NULL >= bound` behaviour.
	undated := BuildWorkDoc(WorkDocInput{ID: 1, DisplayName: "Undated"})
	assert.Zero(t, undated.ReleasedOrd)
	assert.NotContains(t, mustJSON(t, undated), `"released_ord"`)

	// A BODYLESS work still carries claimed=false: omitempty on a bare bool
	// would erase half the population from the claimed facet.
	assert.Contains(t, mustJSON(t, undated), `"claimed":false`)

	// claim_state (A2-R1 区 C) reaches the document verbatim and is filterable —
	// the works search's publishability gate is only as good as the field it
	// filters on being present on EVERY works document, "none" included.
	assert.Equal(t, "live", d.ClaimState)
	assert.Contains(t, mustJSON(t, d), `"claim_state":"live"`)
	none := BuildWorkDoc(WorkDocInput{ID: 2, DisplayName: "Bodyless", ClaimState: "none"})
	assert.Contains(t, mustJSON(t, none), `"claim_state":"none"`)
	assert.Contains(t, WorksFilterableAttributes, "claim_state",
		"a claim_state the index cannot filter on is a gate that silently does nothing")
}

// TestBuildTagDoc pins the tags-index projection (A2-1d).
func TestBuildTagDoc(t *testing.T) {
	d := BuildTagDoc(TagDocInput{
		ID: 12, Name: "純愛", Tier: 1, Kind: 1, WorkCount: 6,
		Sources: []string{"galgame_wiki:311"}, SourceKeys: []string{"galgame_wiki"},
	})
	assert.Equal(t, "t12", d.ID)
	assert.Equal(t, "tag", d.EntityType)
	assert.Equal(t, "純愛", d.NameZh, "han-only names bucket to zh")
	require.NotNil(t, d.Tier)
	require.NotNil(t, d.Kind)
	assert.EqualValues(t, 1, *d.Tier)
	assert.EqualValues(t, 1, *d.Kind)
	assert.InDelta(t, 1.9459, d.Popularity, 0.001, "work count is log-damped like a credit count")
	assert.Equal(t, []string{"galgame_wiki"}, d.SourceKeys)
}

func TestGuessLang(t *testing.T) {
	assert.Equal(t, "ja", GuessLang("いろとりどり"))
	assert.Equal(t, "ja", GuessLang("ソード"))
	assert.Equal(t, "zh", GuessLang("純愛"))
	assert.Equal(t, "en", GuessLang("Sekai Project"))
}

func TestWorkDocIDRoundTrip(t *testing.T) {
	id, ok := WorkDocIDToWorkID(WorkDocID(19658))
	require.True(t, ok)
	assert.EqualValues(t, 19658, id)
	for _, bad := range []string{"", "w", "b12", "wabc", "w0", "w-3"} {
		_, ok := WorkDocIDToWorkID(bad)
		assert.Falsef(t, ok, "%q must not parse as a work doc id", bad)
	}
}

func TestEscapeFilterValue(t *testing.T) {
	assert.Equal(t, `zh\'Hans`, EscapeFilterValue(`zh'Hans`))
	assert.Equal(t, `a\\b`, EscapeFilterValue(`a\b`))
	assert.Equal(t, "ja", EscapeFilterValue("ja"))
}

// TestWorksIndexSettingsCarryTheSearchContract reads the live settings back and
// asserts they contain every attribute the product-search face filters, facets
// or sorts on. A filter whose attribute is not filterable is a Meilisearch 400
// at runtime — this catches it at build time instead.
//
// It also pins maxTotalHits above the registry population: at the 1000 default
// `total` would silently report 1000 for every larger result set, breaking the
// wave's promise that total is the size of the set the caller can page through.
func TestWorksIndexSettingsCarryTheSearchContract(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		for _, uid := range []string{IndexWorks, IndexTags} {
			_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(uid))
		}
	})

	s, err := testClient.Index(IndexWorks).GetSettings()
	require.NoError(t, err)
	for _, attr := range []string{
		"id", "content_rating", "claimed", "olang",
		"tag_ids", "label_ids", "engine_ids", "series_ids", "released_ord", "source_keys",
	} {
		assert.Containsf(t, s.FilterableAttributes, attr, "works index must filter on %s", attr)
	}
	for _, attr := range []string{"popularity", "released_ord", "updated_ts"} {
		assert.Containsf(t, s.SortableAttributes, attr, "works index must sort on %s", attr)
	}
	require.NotNil(t, s.Pagination)
	assert.GreaterOrEqual(t, s.Pagination.MaxTotalHits, int64(300_000),
		"maxTotalHits must exceed the live galgame population or total under-reports")

	// The tags index (A2-1d) mirrors the labels shape plus tier.
	ts, err := testClient.Index(IndexTags).GetSettings()
	require.NoError(t, err)
	assert.Contains(t, ts.FilterableAttributes, "tier")
	assert.Contains(t, ts.FilterableAttributes, "kind")
	assert.Contains(t, ts.SearchableAttributes, "name_zh")
	assert.Contains(t, ts.SortableAttributes, "popularity")

	// EnsureIndexes is the idempotent carrier of a settings change: running it
	// again must converge to the same terminal state, which is what lets the
	// reindex cron ship this wave with no manual Meilisearch step.
	require.NoError(t, EnsureIndexes(testClient))
	again, err := testClient.Index(IndexWorks).GetSettings()
	require.NoError(t, err)
	assert.ElementsMatch(t, s.FilterableAttributes, again.FilterableAttributes)
	assert.ElementsMatch(t, s.SortableAttributes, again.SortableAttributes)
	require.NotNil(t, again.Pagination)
	assert.Equal(t, s.Pagination.MaxTotalHits, again.Pagination.MaxTotalHits)
}

// TestTagsIndexSearchable indexes two canonical tags and searches them through
// the same door the four existing families use.
func TestTagsIndexSearchable(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexTags))
	})

	docs := []EntityDoc{
		BuildTagDoc(TagDocInput{ID: 1, Name: "純愛", Tier: 0, Kind: 0, WorkCount: 100}),
		BuildTagDoc(TagDocInput{ID: 2, Name: "ファンタジー", Tier: 1, Kind: 0, WorkCount: 5}),
	}
	task, err := testClient.Index(IndexTags).AddDocuments(docs, nil)
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)

	idx := NewIndexer(testClient)
	res, err := idx.SearchEntities(t.Context(), IndexTags, "ファンタジー", []string{"jpn"}, 20, "")
	require.NoError(t, err)
	if assert.Len(t, res.Hits, 1) {
		assert.Equal(t, "t2", res.Hits[0].ID)
		require.NotNil(t, res.Hits[0].Tier)
		assert.EqualValues(t, 1, *res.Hits[0].Tier)
	}
	// Empty query → popularity order, so the widely-used tag leads.
	res, err = idx.SearchEntities(t.Context(), IndexTags, "", nil, 20, "")
	require.NoError(t, err)
	if assert.Len(t, res.Hits, 2) {
		assert.Equal(t, "t1", res.Hits[0].ID)
	}
	// tier is filterable.
	res, err = idx.SearchEntities(t.Context(), IndexTags, "", nil, 20, "tier = 1")
	require.NoError(t, err)
	if assert.Len(t, res.Hits, 1) {
		assert.Equal(t, "t2", res.Hits[0].ID)
	}
	// IndexForType routes the public token.
	uid, ok := IndexForType("tags")
	assert.True(t, ok)
	assert.Equal(t, IndexTags, uid)
}

// TestSearchWorksPaging pins the page-based envelope: totalHits is exact over
// the filter, pages do not overlap, and a page past the end is empty rather
// than an error.
func TestSearchWorksPaging(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexWorks))
	})

	docs := make([]EntityDoc, 0, 5)
	for i := 1; i <= 5; i++ {
		rating := int16(0)
		if i == 5 {
			rating = 2
		}
		docs = append(docs, BuildWorkDoc(WorkDocInput{
			ID: int64(i), DisplayName: "作品" + string(rune('A'+i-1)),
			ContentRating: rating, Popularity: float64(i), UpdatedTS: int64(i),
		}))
	}
	task, err := testClient.Index(IndexWorks).AddDocuments(docs, nil)
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)

	idx := NewIndexer(testClient)
	ctx := t.Context()
	q := WorksQuery{Filter: "content_rating != 2", Sort: "popularity:desc", Page: 1, Limit: 2}

	p1, err := idx.SearchWorks(ctx, q)
	require.NoError(t, err)
	assert.EqualValues(t, 4, p1.Total, "total counts the filtered set, not the index")
	assert.Equal(t, []int64{4, 3}, p1.IDs)

	q.Page = 2
	p2, err := idx.SearchWorks(ctx, q)
	require.NoError(t, err)
	assert.EqualValues(t, 4, p2.Total)
	assert.Equal(t, []int64{2, 1}, p2.IDs)

	q.Page = 3
	p3, err := idx.SearchWorks(ctx, q)
	require.NoError(t, err)
	assert.Empty(t, p3.IDs, "a page past the end is empty, not an error")
	assert.EqualValues(t, 4, p3.Total)

	// Facets ride the same filter.
	q.Page, q.Facets = 1, []string{"content_rating"}
	withFacets, err := idx.SearchWorks(ctx, q)
	require.NoError(t, err)
	require.Contains(t, withFacets.Facets, "content_rating")
	assert.EqualValues(t, 4, withFacets.Facets["content_rating"]["0"])
	assert.NotContains(t, withFacets.Facets["content_rating"], "2", "the excluded rating must not appear in the distribution")
}
