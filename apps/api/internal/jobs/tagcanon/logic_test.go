package tagcanon

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
)

// TestNormalize pins the grouping key: NFKC compatibility fold (fullwidth
// parens/letters → ASCII), simple casefold, and trim. Idempotent.
func TestNormalize(t *testing.T) {
	assert.Equal(t, "galgame", normalize("Galgame"))
	assert.Equal(t, "galgame", normalize("  galgame  "), "trim")
	assert.Equal(t, "冒险游戏(adv)", normalize("冒险游戏（ADV）"), "NFKC folds fullwidth（）→() and ADV→adv")
	assert.Equal(t, "bl", normalize("ＢＬ"), "fullwidth latin folds")
	assert.Equal(t, normalize("Galgame"), normalize(normalize("Galgame")), "idempotent")
}

// TestBgmJunk covers the mechanical prefilter: pure number, date, decade, disc /
// season number, and maker/label collision.
func TestBgmJunk(t *testing.T) {
	labels := map[string]struct{}{"avantgarde": {}, "ディーゼルマイン": {}}
	cases := []struct{ in, reason string }{
		{"2024", "number"},
		{"123", "number"},
		{"2024年", "date"},
		{"2024-01", "date"},
		{"2024/1/5", "date"},
		{"2020s", "decade"},
		{"s01", "disc"},
		{"盘2", "disc"},
		{"disc 3", "disc"},
		{"avantgarde", "label"},
		{"ディーゼルマイン", "label"},
		{"百合", ""},        // content — kept
		{"r18", ""},       // meta but NOT junk (kept, stamped kind later)
		{"18x", ""},       // not caught by number regex (has X) — meta, kept
		{"巨乳", ""},        // content
		{"东方project", ""}, // franchise — kept
	}
	for _, c := range cases {
		assert.Equalf(t, c.reason, bgmJunk(c.in, labels), "bgmJunk(%q)", c.in)
	}
}

// TestBuildGroups pins the core decision: only norms spanning ≥2 distinct
// sources become a group; single-source and junk names are excluded; meta norms
// are stamped kind=meta; the canonical name is the form the most sources agree
// on; every group is tier=core.
func TestBuildGroups(t *testing.T) {
	const (
		vndb = int16(2)
		bgm  = int16(3)
		dl   = int16(4)
	)
	vocab := []vocabEntry{
		// cross-source content group (3 sources)
		{SourceID: vndb, Name: "奇幻", Norm: "奇幻", Usage: 900},
		{SourceID: bgm, Name: "奇幻", Norm: "奇幻", Usage: 40},
		{SourceID: dl, Name: "奇幻", Norm: "奇幻", Usage: 1100},
		// cross-source group with a casefold multi-form member (canonical pick)
		{SourceID: vndb, Name: "galgame", Norm: "galgame", Usage: 5},
		{SourceID: bgm, Name: "galgame", Norm: "galgame", Usage: 4},
		{SourceID: bgm, Name: "Galgame", Norm: "galgame", Usage: 109},
		// cross-source META group
		{SourceID: bgm, Name: "像素", Norm: "像素", Usage: 3},
		{SourceID: dl, Name: "像素", Norm: "像素", Usage: 8},
		// single-source name → NO group
		{SourceID: vndb, Name: "巨乳女主角", Norm: "巨乳女主角", Usage: 500},
		// junk (bangumi) → excluded even though dlsite shares the norm
		{SourceID: bgm, Name: "2024", Norm: "2024", Usage: 2, Junk: true, JunkReason: "number"},
		{SourceID: dl, Name: "2024", Norm: "2024", Usage: 1},
	}
	groups := buildGroups(vocab)

	byNorm := map[string]group{}
	for _, g := range groups {
		byNorm[g.Norm] = g
	}
	assert.Len(t, groups, 3, "奇幻 / galgame / 像素 — single-source and junk excluded")
	assert.Contains(t, byNorm, "奇幻")
	assert.Equal(t, 3, byNorm["奇幻"].sourceCount, "spans all three sources")
	assert.EqualValues(t, model.TagTierCore, byNorm["奇幻"].Tier, "cross-source = core")
	assert.EqualValues(t, model.TagKindContent, byNorm["奇幻"].Kind)

	assert.Contains(t, byNorm, "galgame")
	assert.Equal(t, "galgame", byNorm["galgame"].CanonicalName, "the form 2 sources agree on wins over Galgame (1 source, higher usage)")
	assert.Len(t, byNorm["galgame"].Members, 3, "each (source,original) is its own map row — Galgame + galgame both under bangumi")

	assert.Contains(t, byNorm, "像素")
	assert.EqualValues(t, model.TagKindMeta, byNorm["像素"].Kind, "hand-pinned meta")

	assert.NotContains(t, byNorm, "巨乳女主角", "single-source excluded")
	assert.NotContains(t, byNorm, "2024", "bangumi junk excluded from grouping")
}

// TestSingleSourceDist pins the cumulative usage buckets and the top-sample cap
// over the names that did NOT converge into a cross-source group.
func TestSingleSourceDist(t *testing.T) {
	const vndb = int16(2)
	vocab := []vocabEntry{
		{SourceID: vndb, Name: "a", Norm: "a", Usage: 150}, // ge100
		{SourceID: vndb, Name: "b", Norm: "b", Usage: 60},  // ge50
		{SourceID: vndb, Name: "c", Norm: "c", Usage: 25},  // ge20
		{SourceID: vndb, Name: "d", Norm: "d", Usage: 12},  // ge10
		{SourceID: vndb, Name: "e", Norm: "e", Usage: 3},   // below all
		{SourceID: vndb, Name: "grouped", Norm: "shared", Usage: 999, Junk: false},
		{SourceID: 4, Name: "grouped", Norm: "shared", Usage: 1},
		{SourceID: vndb, Name: "junky", Norm: "junky", Usage: 999, Junk: true},
	}
	groups := buildGroups(vocab) // "shared" spans 2 sources → grouped, excluded from dist
	dist := singleSourceDist(vocab, groups, map[int16]string{vndb: "vndb", 4: "dlsite"})

	var v SourceDist
	for _, d := range dist {
		if d.SourceID == vndb {
			v = d
		}
	}
	assert.Equal(t, 1, v.Buckets.GE100)
	assert.Equal(t, 2, v.Buckets.GE50, "cumulative: 150 + 60")
	assert.Equal(t, 3, v.Buckets.GE20)
	assert.Equal(t, 4, v.Buckets.GE10)
	assert.Equal(t, 5, v.Buckets.Total, "junk + grouped excluded, e counted")
	assert.Equal(t, "a", v.Top[0].Name, "highest usage first")
}
