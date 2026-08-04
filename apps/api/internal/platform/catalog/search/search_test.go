package search

import (
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/infrastructure/search"
	"api/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests against a real Meilisearch. Skip if unreachable. Every
// index is created under the test_ prefix so prod/dev indexes are untouched.
var testClient *search.Client

const testPrefix = "test_"

func TestMain(m *testing.M) {
	host := os.Getenv("MEILISEARCH_TEST_HOST")
	if host == "" {
		host = "http://127.0.0.1:7700"
	}
	client, err := search.NewClient(config.MeilisearchConfig{
		Host: host, APIKey: os.Getenv("MEILISEARCH_TEST_API_KEY"), IndexPrefix: testPrefix,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: meilisearch client: %v\n", err)
		os.Exit(0)
	}
	if err := client.Health(); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: meilisearch unreachable: %v\n", err)
		os.Exit(0)
	}
	testClient = client
	os.Exit(m.Run())
}

// TestEnsureIndexesMatchesMatrix creates the indexes then reads their settings
// back and asserts they equal the doc-13 config matrix (guards against a
// slipped field).
func TestEnsureIndexesMatchesMatrix(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		for _, uid := range []string{IndexCreditNames, IndexCharacters, IndexLabels, IndexWorks, IndexTags} {
			_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(uid))
		}
	})

	for _, uid := range []string{IndexCreditNames, IndexCharacters, IndexLabels, IndexWorks, IndexTags} {
		s, err := testClient.Index(uid).GetSettings()
		require.NoError(t, err, uid)

		// localizedAttributes: *_ja→jpn, *_zh→cmn (invariant 1) — except the
		// works index, which pins NOTHING (wave 158: its callers cannot pin the
		// query side, and a half-pinned pair loses CJK recall outright).
		locales := map[string][]string{}
		for _, la := range s.LocalizedAttributes {
			for _, p := range la.AttributePatterns {
				locales[p] = la.Locales
			}
		}
		if uid == IndexWorks {
			assert.Empty(t, s.LocalizedAttributes, uid)
			assert.Nil(t, LocalesForUI(uid, "zh"), "the works lane must never pin a query locale either")
		} else {
			assert.Equal(t, []string{"jpn"}, locales["*_ja"], uid)
			assert.Equal(t, []string{"cmn"}, locales["*_zh"], uid)
		}

		// CJK name fields have typo disabled; latin does not (invariant 3).
		require.NotNil(t, s.TypoTolerance, uid)
		assert.Contains(t, s.TypoTolerance.DisableOnAttributes, "name_ja", uid)
		assert.Contains(t, s.TypoTolerance.DisableOnAttributes, "name_zh", uid)
		assert.NotContains(t, s.TypoTolerance.DisableOnAttributes, "latin", uid)

		// Every index whose entity HAS aliases searches the alias fields, not
		// just the display names. The matrix never asserted the searchable list,
		// which is how the labels and characters indexes shipped matching only
		// name_*: `Yuzusoft` returned nothing though the label listed it as an
		// alias, and 14,845 label + 168,655 character aliases sat in the
		// documents as fields nobody could search.
		//
		// Tags are excluded because they genuinely have no alias table — the
		// canonical-tag map is how a tag gets its synonyms (TestTagsIndexSearchable
		// pins that list separately).
		if uid != IndexTags {
			for _, f := range []string{"aliases_ja", "aliases_zh", "aliases_other"} {
				assert.Contains(t, s.SearchableAttributes, f, uid)
			}
			// Display names rank above aliases, which rank above the
			// romanization (Meilisearch ranks earlier attributes higher).
			assert.Less(t, indexOf(s.SearchableAttributes, "name_ja"),
				indexOf(s.SearchableAttributes, "aliases_ja"), uid)
			assert.Less(t, indexOf(s.SearchableAttributes, "aliases_other"),
				indexOf(s.SearchableAttributes, "latin"), uid)
		}

		// popularity is sortable, and is the LAST ranking rule (after
		// exactness), so it never outranks an exact match (invariant 7).
		assert.Contains(t, s.SortableAttributes, "popularity", uid)
		rr := s.RankingRules
		require.NotEmpty(t, rr, uid)
		assert.Equal(t, "popularity:desc", rr[len(rr)-1], uid)
		assert.Less(t, indexOf(rr, "exactness"), indexOf(rr, "popularity:desc"), uid)
		assert.Less(t, indexOf(rr, "words"), indexOf(rr, "sort"), uid)
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// TestWorksNSFWFilter pins the wave-105 works lane: content_rating is
// filterable (the public nsfw gate) and title aliases match.
func TestWorksNSFWFilter(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexWorks))
	})

	r18, safe := int16(2), int16(0)
	docA := EntityDoc{ID: "w1", EntityType: "work", ContentRating: &r18, Popularity: 3}
	docA.SetNameOrAlias("ja", "いろとりどりのセカイ")
	docA.SetNameOrAlias("zh", "五彩斑斓的世界")
	docB := EntityDoc{ID: "w2", EntityType: "work", ContentRating: &safe, Popularity: 1}
	docB.SetNameOrAlias("ja", "全年齢作品")

	task, err := testClient.Index(IndexWorks).AddDocuments([]EntityDoc{docA, docB}, nil)
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)

	idx := NewIndexer(testClient)
	ctx := t.Context()

	// nsfw off: the r18 hit is excluded server-side.
	res, err := idx.SearchEntities(ctx, IndexWorks, "いろとりどり", []string{"jpn"}, 20, "content_rating != 2")
	require.NoError(t, err)
	assert.Len(t, res.Hits, 0, "r18 filtered")
	// nsfw on: no filter, the alias (zh bucket) matches too.
	res, err = idx.SearchEntities(ctx, IndexWorks, "五彩斑斓", []string{"cmn"}, 20, "")
	require.NoError(t, err)
	if assert.Len(t, res.Hits, 1) {
		assert.Equal(t, "w1", res.Hits[0].ID)
		require.NotNil(t, res.Hits[0].ContentRating)
		assert.EqualValues(t, 2, *res.Hits[0].ContentRating)
	}
	// empty query + filter: popularity sort, safe doc only.
	res, err = idx.SearchEntities(ctx, IndexWorks, "", nil, 20, "content_rating != 2")
	require.NoError(t, err)
	if assert.Len(t, res.Hits, 1) {
		assert.Equal(t, "w2", res.Hits[0].ID)
	}
}

// TestLabelAliasIsSearchable is the forum-reported symptom, pinned: the label
// ゆずソフト carries "Yuzusoft" as an alias, and a search for that alias found
// nothing because the labels index searched only display names.
func TestLabelAliasIsSearchable(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexLabels))
	})

	kind := int16(4)
	doc := EntityDoc{ID: "b5147", EntityType: "label", Kind: &kind}
	doc.SetName("ja", "ゆずソフト")
	for _, a := range []string{"Yuzu-Soft", "Yuzusoft", "ユズソフト"} {
		doc.AddAlias("en", a)
	}
	doc.AddAlias("zh", "柚子社")

	task, err := testClient.Index(IndexLabels).AddDocuments([]EntityDoc{doc}, nil)
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)

	idx := NewIndexer(testClient)
	for _, q := range []string{"Yuzusoft", "yuzusoft", "柚子社", "ゆずソフト"} {
		res, err := idx.SearchEntities(t.Context(), IndexLabels, q, nil, 20, "")
		require.NoError(t, err, q)
		if assert.Len(t, res.Hits, 1, "query %q found nothing", q) {
			assert.Equal(t, "b5147", res.Hits[0].ID, q)
		}
	}
}

// TestDeleteBatchRemovesTombstones pins the purge half. Skipping soft-deleted
// rows in the build loop is not enough on its own: the reindexer upserts and
// never clears the index, so a document written before its row was merged away
// outlives every later run. Three identical 「ゆずソフト」 labels in one result
// list — two of them dead ids — is what that looked like in production.
func TestDeleteBatchRemovesTombstones(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() {
		_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexLabels))
	})

	live := EntityDoc{ID: "b5147", EntityType: "label"}
	live.SetName("ja", "ゆずソフト")
	dead := EntityDoc{ID: "b96", EntityType: "label"}
	dead.SetName("ja", "ゆずソフト")

	task, err := testClient.Index(IndexLabels).AddDocuments([]EntityDoc{live, dead}, nil)
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)

	idx := NewIndexer(testClient)
	ctx := t.Context()
	res, err := idx.SearchEntities(ctx, IndexLabels, "ゆずソフト", []string{"jpn"}, 20, "")
	require.NoError(t, err)
	require.Len(t, res.Hits, 2, "both are indexed before the purge")

	require.NoError(t, idx.DeleteBatch(ctx, IndexLabels, []string{"b96"}))
	require.Eventually(t, func() bool {
		res, err := idx.SearchEntities(ctx, IndexLabels, "ゆずソフト", []string{"jpn"}, 20, "")
		return err == nil && len(res.Hits) == 1 && res.Hits[0].ID == "b5147"
	}, 10*time.Second, 100*time.Millisecond, "the merged label is gone, the survivor stays")

	// Re-running a purge for an id the index no longer holds is a no-op, so the
	// nightly can issue the same delete list every night.
	require.NoError(t, idx.DeleteBatch(ctx, IndexLabels, []string{"b96"}))
	require.NoError(t, idx.DeleteBatch(ctx, IndexLabels, nil))
}
