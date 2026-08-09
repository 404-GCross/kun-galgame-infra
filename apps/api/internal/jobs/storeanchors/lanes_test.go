package storeanchors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizers pins each lane's id shape. The DMM cases are the ones that
// matter: VNDB stores a URL path, and the two page shapes that carry a content
// id must both resolve to the bare cid the catalog already uses, while landing
// and listing pages must resolve to nothing rather than to something id-shaped.
func TestNormalizers(t *testing.T) {
	cases := []struct {
		name string
		lane string
		in   string
		want string
	}{
		// steam
		{"steam appid", LaneSteam, "2258770", "2258770"},
		{"steam appid padded", LaneSteam, "  3987860 ", "3987860"},
		{"steam url rejected", LaneSteam, "store.steampowered.com/app/2258770/", ""},
		{"steam empty", LaneSteam, "", ""},

		// dmm — mono / dc / digital pages carry cid= in the path
		{"dmm mono pcgame", LaneDmm, "www.dmm.co.jp/mono/pcgame/-/detail/=/cid=2212apc13900/", "2212apc13900"},
		{"dmm dc doujin", LaneDmm, "www.dmm.co.jp/dc/doujin/-/detail/=/cid=d_022265/", "d_022265"},
		{"dmm digital shop param", LaneDmm, "www.dmm.co.jp/digital/doujin/-/detail/=/shop=doujin/cid=d_123/", "d_123"},
		{"dmm en prefix", LaneDmm, "www.dmm.co.jp/en/dc/doujin/-/detail/=/cid=d_999/", "d_999"},
		// dlsoft carries the id directly after /detail/
		{"dmm dlsoft", LaneDmm, "dlsoft.dmm.co.jp/detail/vsat_0145/", "vsat_0145"},
		{"dmm dlsoft en", LaneDmm, "dlsoft.dmm.co.jp/en/detail/awc_0006/", "awc_0006"},
		{"dmm dlsoft com", LaneDmm, "dlsoft.dmm.com/detail/next_0031/", "next_0031"},
		// landing / listing / feature pages identify no product
		{"dmm original landing", LaneDmm, "dlsoft.dmm.co.jp/original/yuho/", ""},
		{"dmm feature", LaneDmm, "dlsoft.dmm.com/feature/dmmgames/iwaihime/", ""},
		{"dmm serial", LaneDmm, "dlsoft.dmm.com/serial/views_0721/", ""},
		{"dmm maker list", LaneDmm, "www.dmm.co.jp/dc/doujin/-/list/=/article=maker/id=25344/", ""},
		{"dmm index page", LaneDmm, "www.dmm.com/dc/pcgame/galaxy_angel/index_html/=/ch_navi=/", ""},

		// dlsite JP
		{"dlsite RJ", LaneDlsite, "RJ264149", "RJ264149"},
		{"dlsite VJ", LaneDlsite, "VJ010897", "VJ010897"},
		{"dlsite RE not in JP lane", LaneDlsite, "RE123456", ""},
		{"dlsite url rejected", LaneDlsite, "www.dlsite.com/maniax/work/=/product_id/RJ264149.html", ""},

		// dlsite EN
		{"dlsite EN RE", LaneDlsiteEN, "RE245678", "RE245678"},
		{"dlsite EN rejects RJ", LaneDlsiteEN, "RJ264149", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, laneByName(t, tc.lane).normalize(tc.in))
		})
	}
}

func laneByName(t *testing.T, name string) lane {
	t.Helper()
	ls, err := selectedLanes(name)
	require.NoError(t, err)
	require.Len(t, ls, 1)
	return ls[0]
}

func TestSelectedLanes(t *testing.T) {
	all, err := selectedLanes("")
	require.NoError(t, err)
	assert.Len(t, all, 4, "every lane runs when --only is empty")

	_, err = selectedLanes("getchu")
	assert.Error(t, err, "getchu belongs to the wave-167 importer, not to this one")
}

// TestDecideFilters exercises the whole filtering doctrine without a database:
// every candidate must end up either in the plan or in exactly one skip
// counter.
func TestDecideFilters(t *testing.T) {
	l := laneByName(t, LaneSteam)
	cands := []candidate{
		{ReleaseID: 1, WorkID: 10, RawValue: "111"},   // clean
		{ReleaseID: 2, WorkID: 20, RawValue: "store"}, // malformed
		{ReleaseID: 3, WorkID: 30, RawValue: "333"},   // rejected
		{ReleaseID: 4, WorkID: 40, RawValue: "444"},   // already held exact
		{ReleaseID: 5, WorkID: 50, RawValue: "555"},   // ambiguous …
		{ReleaseID: 6, WorkID: 60, RawValue: "555"},   // … with this one
		{ReleaseID: 7, WorkID: 70, RawValue: "777"},   // dup pair …
		{ReleaseID: 7, WorkID: 70, RawValue: "777"},   // … same release+id twice
	}
	taken := map[string]struct{}{"444": {}}
	rejected := map[string]struct{}{rejKey(3, "333"): {}}

	ls := &LaneStats{Candidates: len(cands)}
	plan := decide(cands, l, taken, rejected, ls)

	assert.Equal(t, 1, ls.SkippedMalformed)
	assert.Equal(t, 1, ls.SkippedRejection)
	assert.Equal(t, 1, ls.SkippedValueTaken)
	assert.Equal(t, 2, ls.SkippedAmbiguous, "BOTH holders are skipped — no arbitrary winner")
	assert.Equal(t, 1, ls.SkippedDedup)
	require.Len(t, plan, 2)
	assert.Equal(t, []plannedRef{
		{releaseID: 1, workID: 10, externalID: "111"},
		{releaseID: 7, workID: 70, externalID: "777"},
	}, plan)

	accounted := len(plan) + ls.SkippedMalformed + ls.SkippedRejection +
		ls.SkippedValueTaken + ls.SkippedAmbiguous + ls.SkippedDedup
	assert.Equal(t, len(cands), accounted, "every candidate is accounted for exactly once")
}

// TestAmbiguityBeatsOrdering pins that ambiguity is detected in a first pass:
// whichever order the candidates arrive in, neither holder is planned.
func TestAmbiguityBeatsOrdering(t *testing.T) {
	l := laneByName(t, LaneDmm)
	cands := []candidate{
		{ReleaseID: 9, WorkID: 90, RawValue: "dlsoft.dmm.co.jp/detail/next_0352/"},
		{ReleaseID: 8, WorkID: 80, RawValue: "www.dmm.co.jp/mono/pcgame/-/detail/=/cid=next_0352/"},
	}
	ls := &LaneStats{}
	plan := decide(cands, l, map[string]struct{}{}, map[string]struct{}{}, ls)
	assert.Empty(t, plan)
	assert.Equal(t, 2, ls.SkippedAmbiguous)
	assert.Contains(t, ls.AmbiguousSamples, "next_0352",
		"both URL shapes normalize to the same cid, which is what makes them ambiguous")
}
