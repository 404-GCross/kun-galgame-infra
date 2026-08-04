package getchuintros

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A work with several Getchu releases must be judged on the BEST of them. The
// obvious SQL shortcut (DISTINCT ON, lowest id) would read the first anchor and
// report the work unreachable whenever that page happens to be the one Getchu
// never gave a story — which is why the choice is made here, against the
// staging map, instead of in the candidate query.
func TestPickStoryPrefersTheAnchorThatHasText(t *testing.T) {
	anchors := []anchorRow{
		{WorkID: 1, GetchuID: "100"}, // 'gone' — no row in the staging map
		{WorkID: 1, GetchuID: "200"}, // the DL edition, crawled with a story
		{WorkID: 2, GetchuID: "300"},
	}
	stories := map[string]string{"200": "  物語です  ", "300": "another"}

	got := pickStory(anchors, stories)
	require.Len(t, got, 2)
	assert.Equal(t, "200", got[0].GetchuID)
	assert.Equal(t, "物語です", got[0].Story, "surrounding whitespace is trimmed")
	assert.Equal(t, "300", got[1].GetchuID)
}

// Among several anchors that all have text the lowest Getchu id wins, so
// re-runs are stable and a diff between two runs means the DATA changed.
func TestPickStoryIsDeterministic(t *testing.T) {
	anchors := []anchorRow{{WorkID: 1, GetchuID: "100"}, {WorkID: 1, GetchuID: "200"}}
	stories := map[string]string{"100": "first", "200": "second"}

	got := pickStory(anchors, stories)
	require.Len(t, got, 1)
	assert.Equal(t, "first", got[0].Story)
}

// A work whose every anchor is storyless still appears, with an empty Story.
// Dropping it here would hide the reach of the crawl: the run must be able to
// report no_story rather than silently shrinking its own denominator.
func TestPickStoryKeepsUnreachableWorks(t *testing.T) {
	got := pickStory([]anchorRow{{WorkID: 7, GetchuID: "1"}}, map[string]string{"2": "x"})
	require.Len(t, got, 1)
	assert.Equal(t, int64(7), got[0].WorkID)
	assert.Empty(t, got[0].Story)
}

// Whitespace-only story text is not text. Getchu pages with an empty story
// block would otherwise land a blank intro row, which reads worse than none.
func TestPickStoryIgnoresBlankText(t *testing.T) {
	got := pickStory([]anchorRow{{WorkID: 1, GetchuID: "1"}}, map[string]string{"1": "   \n\t "})
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Story)
}

// Chunking windows WORKS, not anchor rows. A work whose two releases straddled
// a naive row-offset boundary would be processed — and counted — by both chunks.
func TestWindowCountsWorksNotRows(t *testing.T) {
	rows := []anchorRow{
		{WorkID: 1, GetchuID: "a"}, {WorkID: 1, GetchuID: "b"},
		{WorkID: 2, GetchuID: "c"},
		{WorkID: 3, GetchuID: "d"}, {WorkID: 3, GetchuID: "e"},
	}
	assert.Equal(t, rows, window(rows, 0, 0), "no window is a passthrough")

	first := window(rows, 2, 0)
	assert.Equal(t, []anchorRow{rows[0], rows[1], rows[2]}, first, "two works, three rows")

	second := window(rows, 2, 2)
	assert.Equal(t, []anchorRow{rows[3], rows[4]}, second, "resumes at work 3, not row 3")

	assert.Empty(t, window(rows, 2, 9), "an offset past the end yields nothing")
}
