package tagcanon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tVndb = int16(2)
	tBgm  = int16(3)
	tDl   = int16(4)
)

func cn(src int16, key, name string, usage int, works ...int64) candName {
	c := candName{SourceID: src, SourceKey: key, Name: name, Norm: normalize(name), Orig: name, Usage: usage}
	if len(works) > 0 {
		c.workIDs = map[int64]struct{}{}
		for _, w := range works {
			c.workIDs[w] = struct{}{}
		}
	}
	return c
}

func TestBuildCandidatePairs(t *testing.T) {
	pool := []candName{
		cn(tVndb, "vndb", "巨乳", 300),
		cn(tDl, "dlsite", "巨乳/爆乳", 1500),
		cn(tVndb, "vndb", "学园", 200),
		cn(tDl, "dlsite", "学院", 400),
		cn(tBgm, "bangumi", "银发", 90, 1, 2, 3, 4),
		cn(tDl, "dlsite", "白毛", 80, 1, 2, 3, 5),
		cn(tVndb, "vndb", "开朗", 50),
		cn(tVndb, "vndb", "开郎", 50),
		cn(tDl, "dlsite", "萝", 900),
	}
	pairs, st := buildCandidatePairs(pool, blockOpts{})

	got := map[string]string{}
	for _, p := range pairs {
		got[p.A.Name+"|"+p.B.Name] = p.Block
	}
	assert.Equal(t, "substring", got["巨乳|巨乳/爆乳"], "vndb 巨乳 ⊂ dlsite 巨乳/爆乳")
	assert.Equal(t, "edit", got["学园|学院"], "one-rune edit")
	assert.Equal(t, "cooccur", got["银发|白毛"], "bgm↔dlsite work overlap 3/5")
	assert.NotContains(t, got, "开朗|开郎", "same-source (both vndb) never pairs")

	assert.Equal(t, 1, st.Substring)
	assert.Equal(t, 1, st.Edit)
	assert.Equal(t, 1, st.Cooccur)
	assert.Equal(t, 3, st.Total)

	pairs2, _ := buildCandidatePairs(pool, blockOpts{})
	require.Len(t, pairs2, len(pairs))
	for i := range pairs {
		assert.Equal(t, pairs[i].A.Name, pairs2[i].A.Name)
		assert.Equal(t, pairs[i].B.Name, pairs2[i].B.Name)
	}
}

func TestLevenshtein(t *testing.T) {
	assert.Equal(t, 0, levenshtein("学园", "学园"))
	assert.Equal(t, 1, levenshtein("学园", "学院"))
	assert.Equal(t, 2, levenshtein("abc", "axy"))
	assert.Equal(t, 3, levenshtein("", "xyz"))
}

func TestMockMatcher(t *testing.T) {
	m := MockMatcher{Model: "glm"}
	ctx := context.Background()

	v, mdl, err := m.MatchPair(ctx, PairInput{AOrig: "Yuri", AName: "百合", BOrig: "Yuri", BName: "百合"})
	require.NoError(t, err)
	assert.Equal(t, RelExact, v.Relation)
	assert.Equal(t, "mock:glm", mdl)

	v, _, _ = m.MatchPair(ctx, PairInput{AName: "巨乳/爆乳", BName: "巨乳"})
	assert.Equal(t, RelBroader, v.Relation, "A contains B → A broader")

	v, _, _ = m.MatchPair(ctx, PairInput{AName: "巨乳", BName: "巨乳/爆乳"})
	assert.Equal(t, RelNarrower, v.Relation, "B contains A → A narrower")

	v, _, _ = m.MatchPair(ctx, PairInput{AName: "学园", BName: "学院"})
	assert.Equal(t, RelRelated, v.Relation, "edit distance 1 → related")

	v, _, _ = m.MatchPair(ctx, PairInput{AName: "机器人", BName: "料理"})
	assert.Equal(t, RelUnrelated, v.Relation)

	nv, _, _ := m.ClassifyName(ctx, NameInput{Name: "R18", Usage: 500})
	assert.EqualValues(t, model.TagKindMeta, nv.Kind)
	assert.EqualValues(t, model.TagTierHidden, nv.Tier, "meta → hidden")

	nv, _, _ = m.ClassifyName(ctx, NameInput{Name: "破处", Usage: 5000})
	assert.EqualValues(t, model.TagKindContent, nv.Kind)
	assert.EqualValues(t, model.TagTierCore, nv.Tier, "high-usage content → core")

	nv, _, _ = m.ClassifyName(ctx, NameInput{Name: "冷门设定", Usage: 120})
	assert.EqualValues(t, model.TagTierLongtail, nv.Tier, "low-usage content → longtail")
}

func TestMakeReviewBuckets(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "verdicts.jsonl")
	recs := []pairRec{
		{Kind: "pair", ASource: "vndb", AName: "百合", BSource: "bangumi", BName: "百合", Relation: "exact", Confidence: 0.98},
		{Kind: "pair", ASource: "vndb", AName: "学园", BSource: "dlsite", BName: "学院", Relation: "exact", Confidence: 0.75},
		{Kind: "pair", ASource: "vndb", AName: "催眠", BSource: "dlsite", BName: "精神控制", Relation: "narrower", Confidence: 0.9},
		{Kind: "pair", ASource: "vndb", AName: "x", BSource: "dlsite", BName: "y", Relation: "exact", Confidence: 0.3},
		{Kind: "single", Source: "vndb", Name: "破处", Usage: 6000, Tier: i16p(model.TagTierCore), Kind_: i16p(model.TagKindContent), Confidence: 0.95},
		{Kind: "single", Source: "vndb", Name: "主人公露过正脸", Usage: 11000, Tier: i16p(model.TagTierLongtail), Kind_: i16p(model.TagKindMeta), Confidence: 0.7},
	}
	require.NoError(t, writeRecords(in, recs))

	md := filepath.Join(dir, "review.md")
	dec := filepath.Join(dir, "decisions.jsonl")
	st, err := MakeReview(in, md, dec, ReviewOpts{})
	require.NoError(t, err)

	assert.Equal(t, 1, st.HighExact, "0.98 exact")
	assert.Equal(t, 1, st.MediumExact, "0.75 exact")
	assert.Equal(t, 1, st.LowExact, "0.30 exact dropped")
	assert.Equal(t, 1, st.NonExact[RelNarrower], "narrower留档, not merged")
	assert.Equal(t, 1, st.SingleHigh)
	assert.Equal(t, 1, st.SingleMedium)
	assert.Equal(t, 2, st.Approved, "1 high pair + 1 high single")

	out, err := readRecords(dec)
	require.NoError(t, err)
	approved, narrowerSeen := 0, false
	for _, r := range out {
		if r.Approve {
			approved++
		}
		if r.Relation == "narrower" {
			narrowerSeen = true
			assert.False(t, r.Approve, "narrower is留档 only, never approved")
		}
	}
	assert.Equal(t, 2, approved)
	assert.True(t, narrowerSeen)

	body, err := os.ReadFile(md)
	require.NoError(t, err)
	assert.Contains(t, string(body), "学园", "medium pair surfaced for human review")
}
