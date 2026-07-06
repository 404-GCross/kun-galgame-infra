package search

import (
	"fmt"
	"os"
	"testing"

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
		for _, uid := range []string{IndexCreditNames, IndexCharacters, IndexLabels} {
			_, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(uid))
		}
	})

	for _, uid := range []string{IndexCreditNames, IndexCharacters, IndexLabels} {
		s, err := testClient.Index(uid).GetSettings()
		require.NoError(t, err, uid)

		// localizedAttributes: *_ja→jpn, *_zh→cmn (invariant 1).
		locales := map[string][]string{}
		for _, la := range s.LocalizedAttributes {
			for _, p := range la.AttributePatterns {
				locales[p] = la.Locales
			}
		}
		assert.Equal(t, []string{"jpn"}, locales["*_ja"], uid)
		assert.Equal(t, []string{"cmn"}, locales["*_zh"], uid)

		// CJK name fields have typo disabled; latin does not (invariant 3).
		require.NotNil(t, s.TypoTolerance, uid)
		assert.Contains(t, s.TypoTolerance.DisableOnAttributes, "name_ja", uid)
		assert.Contains(t, s.TypoTolerance.DisableOnAttributes, "name_zh", uid)
		assert.NotContains(t, s.TypoTolerance.DisableOnAttributes, "latin", uid)

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
