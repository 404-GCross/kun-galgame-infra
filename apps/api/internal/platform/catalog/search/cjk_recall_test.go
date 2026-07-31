package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSanitizeQueryOperators pins the operator neutralization, fullwidth forms
// included. Meilisearch normalizes '－' (U+FF0D) and '＂' (U+FF02) to the ASCII
// operators BEFORE parsing them, so a Japanese title using the fullwidth dash as
// a subtitle delimiter used to exclude itself from its own search.
func TestSanitizeQueryOperators(t *testing.T) {
	cases := map[string]string{
		// wave 158's 60th title: the fullwidth dash was read as negation.
		"アヘ顔アクメ中毒 －人体改造で狂ってイク私を見ないで－": "アヘ顔アクメ中毒  人体改造で狂ってイク私を見ないで",
		"－人体改造":     "人体改造",
		"＂CLANNAD＂": "CLANNAD",
		// the ASCII forms this guard already covered
		"CRAZY CHA!N -エルピスの鎖-": "CRAZY CHA!N  エルピスの鎖",
		`"CLANNAD"`:            "CLANNAD",
		// letters that merely LOOK like operators must survive: the wave dash
		// and the long-vowel mark carry meaning in Japanese titles.
		"色仕掛け学園～思春期男子誘惑作戦～": "色仕掛け学園～思春期男子誘惑作戦～",
		"ソードアート":     "ソードアート",
		"":           "",
		"  spaced  ": "spaced",
	}
	for in, want := range cases {
		assert.Equal(t, want, sanitizeQuery(in), "in=%q", in)
	}
}

// TestLocalesForUIPairsWithTheIndex pins wave 158's rule: a query locale is only
// correct when the index pinned the same one at write time.
func TestLocalesForUIPairsWithTheIndex(t *testing.T) {
	assert.Equal(t, []string{"cmn"}, LocalesForUI(IndexCharacters, "zh"))
	assert.Equal(t, []string{"jpn"}, LocalesForUI(IndexCreditNames, "ja"))
	assert.Nil(t, LocalesForUI(IndexLabels, "en"))
	// The works index pins nothing, so no caller may pin the query either.
	assert.Nil(t, LocalesForUI(IndexWorks, "zh"))
	assert.Nil(t, LocalesForUI(IndexWorks, "ja"))
}

// TestEnsureIndexesResetsWorksLocalePins guards the omitempty trap: a Settings
// field left nil PATCHes nothing, so an index created under the old spec would
// keep its pins forever unless EnsureIndexes resets them explicitly.
func TestEnsureIndexesResetsWorksLocalePins(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() { _, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexWorks)) })

	task, err := testClient.Index(IndexWorks).UpdateLocalizedAttributes(localizedAttributes())
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)
	got, err := testClient.Index(IndexWorks).GetLocalizedAttributes()
	require.NoError(t, err)
	require.NotEmpty(t, got, "precondition: the stale pins are in place")

	require.NoError(t, EnsureIndexes(testClient))
	got, err = testClient.Index(IndexWorks).GetLocalizedAttributes()
	require.NoError(t, err)
	assert.Empty(t, got, "EnsureIndexes must clear pins it no longer declares")
}

// TestWorksCJKTitleRecall is the wave-158 regression, reduced to four
// documents. Every query here is a work's OWN title (or a word out of its
// middle) — the search a kungal user runs after copying a title from anywhere.
// With `localizedAttributes` back on the index, the first two cases return
// nothing: the index segments kanji with the Japanese pipeline while the query,
// whose language Meilisearch autodetects as Chinese, is segmented with the
// Chinese one, and matchingStrategy=all then finds no common term.
func TestWorksCJKTitleRecall(t *testing.T) {
	require.NoError(t, EnsureIndexes(testClient))
	t.Cleanup(func() { _, _ = testClient.Svc().DeleteIndex(testClient.IndexUID(IndexWorks)) })

	docs := []EntityDoc{
		{ID: "w1", EntityType: "work", NameJa: "色仕掛け学園～思春期男子誘惑作戦～"},
		{ID: "w2", EntityType: "work", NameJa: "マスカレード～地獄学園SO/DO/MU～"},
		{ID: "w3", EntityType: "work", NameJa: "アヘ顔アクメ中毒 －人体改造で狂ってイク私を見ないで－"},
		{ID: "w4", EntityType: "work", NameZh: "装甲恶鬼村正"},
	}
	task, err := testClient.Index(IndexWorks).AddDocuments(docs, nil)
	require.NoError(t, err)
	_, err = testClient.Svc().WaitForTask(task.TaskUID, 0)
	require.NoError(t, err)

	idx := NewIndexer(testClient)
	for _, tc := range []struct{ name, q, want string }{
		{"full ja title, kana + kanji + wave dash", "色仕掛け学園～思春期男子誘惑作戦～", "w1"},
		{"kanji-only word from the middle of a title", "地獄学園", "w2"},
		{"fullwidth dash subtitle must not negate the work", "アヘ顔アクメ中毒 －人体改造で狂ってイク私を見ないで－", "w3"},
		{"a zh title still recalls itself", "装甲恶鬼村正", "w4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := idx.SearchWorks(t.Context(), WorksQuery{Q: tc.q, Limit: 12})
			require.NoError(t, err)
			id, ok := WorkDocIDToWorkID(tc.want)
			require.True(t, ok)
			assert.Contains(t, res.IDs, id, "%q must recall %s", tc.q, tc.want)
		})
	}
}
