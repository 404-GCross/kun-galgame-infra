package tagsafety

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testState() CatalogState {
	return CatalogState{
		Vocab: map[string]VocabRow{
			"调教":   {ID: 1, Sexual: false, Tier: model.TagTierCore},
			"纯爱":   {ID: 2, Sexual: true, Tier: model.TagTierCore},
			"黄油":   {ID: 3, Sexual: false, Tier: model.TagTierHidden},
			"视觉小说": {ID: 4, Sexual: false, Tier: model.TagTierCore},
			"中出":   {ID: 5, Sexual: true, Tier: model.TagTierCore},
		},
		Mapped: map[string]string{
			verdictKey("bangumi", "調教"):  "调教",
			verdictKey("bangumi", "纯爱"):  "纯爱",
			verdictKey("bangumi", "黄油"):  "黄油",
			verdictKey("dlsite", "視覚小説"): "视觉小说",
			verdictKey("bangumi", "中出"):  "中出",
		},
	}
}

func v(source, name, class string, conf float64) Verdict {
	return Verdict{Source: source, Name: name, Class: class, Confidence: conf, Uses: 10, Votes: 20}
}

func TestBuildPlanBuckets(t *testing.T) {
	plan := BuildPlan([]Verdict{
		v("bangumi", "調教", "sexual", 0.96),
		v("bangumi", "拔作", "sexual", 0.98),
		v("bangumi", "中出", "sexual", 0.99),
		v("bangumi", "黄油", "junk", 0.97),
		v("dlsite", "視覚小説", "junk", 0.99),
		v("bangumi", "PC", "junk", 0.99),
		v("bangumi", "校园", "normal", 0.95),
		v("bangumi", "青梅竹马", "sexual", 0.55),
		v("dlsite", "ロリ", "junk", 0.80),
	}, nil, testState(), PlanOpts{MinConfidence: 0.90})

	assert.Equal(t, []SourceName{
		{Source: "bangumi", Name: "中出"},
		{Source: "bangumi", Name: "拔作"},
		{Source: "bangumi", Name: "調教"},
	}, plan.WorkTagSexual, "an unmapped sexual name still flags its verbatim rows")
	assert.Equal(t, []string{"调教"}, plan.VocabSexual, "中出 is already sexual — no churn UPDATE")
	assert.Equal(t, []string{"视觉小说"}, plan.VocabHidden, "黄油 is already hidden; PC is unmapped")

	assert.Equal(t, 1, plan.Counts.AlreadySexual)
	assert.Equal(t, 1, plan.Counts.AlreadyHidden)
	assert.Equal(t, 1, plan.Counts.UnmappedJunk)
	assert.Equal(t, 2, plan.Counts.BelowThreshold)

	require.Len(t, plan.Review, 2)
	assert.Equal(t, "青梅竹马", plan.Review[0].Name)
	assert.Equal(t, "below-confidence", plan.Review[0].Note)
	assert.Equal(t, "ロリ", plan.Review[1].Name)

	assert.Equal(t, 9, plan.Counts.Total)
	assert.Equal(t, 4, plan.Counts.ByClass[ClassSexual], "ByClass counts every verdict, confident or not")
	assert.Equal(t, 4, plan.Counts.ByClass[ClassJunk])
	assert.Equal(t, 1, plan.Counts.ByClass[ClassNormal])
	assert.Equal(t, 3, plan.Counts.Confident[ClassSexual])
	assert.Equal(t, 3, plan.Counts.Confident[ClassJunk])
	assert.Equal(t, 1, plan.Counts.Confident[ClassNormal])
	assert.Equal(t, 7, plan.Counts.Buckets["0.90-1.00"])
	assert.Equal(t, 1, plan.Counts.Buckets["0.70-0.90"])
	assert.Equal(t, 1, plan.Counts.Buckets["0.50-0.70"])
}

func TestBuildPlanDeflagGuard(t *testing.T) {
	plan := BuildPlan([]Verdict{
		v("bangumi", "纯爱", "normal", 0.99),
		v("bangumi", "校园", "normal", 0.99),
		v(VocabSource, "中出", "normal", 0.99),
	}, nil, testState(), PlanOpts{MinConfidence: 0.90})

	assert.Empty(t, plan.VocabSexual)
	assert.Empty(t, plan.VocabHidden)
	assert.Empty(t, plan.WorkTagSexual)
	assert.Equal(t, 2, plan.Counts.DeflagGuard)
	require.Len(t, plan.Review, 2)
	for _, r := range plan.Review {
		assert.Contains(t, r.Note, "deflag-candidate")
	}
	assert.Equal(t, []string{"纯爱", "中出"}, []string{plan.Review[0].Name, plan.Review[1].Name}, "review is sorted by source then name")
}

func TestBuildPlanVocabSource(t *testing.T) {
	plan := BuildPlan([]Verdict{
		v(VocabSource, "调教", "sexual", 0.99),
		v(VocabSource, "视觉小说", "junk", 0.99),
		v(VocabSource, "不存在的规范名", "sexual", 0.99),
	}, nil, testState(), PlanOpts{MinConfidence: 0.90})

	assert.Empty(t, plan.WorkTagSexual, "the canonical vocabulary has no verbatim rows")
	assert.Equal(t, []string{"调教"}, plan.VocabSexual)
	assert.Equal(t, []string{"视觉小说"}, plan.VocabHidden)
}

func TestBuildPlanReviewedFullTrust(t *testing.T) {
	plan := BuildPlan([]Verdict{
		v("bangumi", "調教", "normal", 0.99),
		v("bangumi", "拔作", "sexual", 0.30),
	}, []ReviewedLine{
		{Source: "bangumi", Name: "調教", Class: "sexual", Reason: "human: 明确成人向"},
		{Source: "bangumi", Name: "拔作", Class: "sexual"},
		{Source: VocabSource, Name: "视觉小说", Class: "junk"},
	}, testState(), PlanOpts{MinConfidence: 0.90})

	assert.Equal(t, 3, plan.Counts.Reviewed)
	assert.Equal(t, []SourceName{
		{Source: "bangumi", Name: "拔作"},
		{Source: "bangumi", Name: "調教"},
	}, plan.WorkTagSexual, "a 0.30 verdict still applies once a human ruled it")
	assert.Equal(t, []string{"调教"}, plan.VocabSexual)
	assert.Equal(t, []string{"视觉小说"}, plan.VocabHidden, "a ruled name the model never saw still applies")
	assert.Empty(t, plan.Review)
}

func TestApplyLimitTruncatesWritesNotReview(t *testing.T) {
	verdicts := []Verdict{
		v("bangumi", "調教", "sexual", 0.99),
		v("bangumi", "拔作", "sexual", 0.99),
		v("dlsite", "視覚小説", "junk", 0.99),
		v("bangumi", "青梅竹马", "sexual", 0.10),
		v("dlsite", "ロリ", "sexual", 0.10),
	}
	full := BuildPlan(verdicts, nil, testState(), PlanOpts{MinConfidence: 0.90})
	require.Equal(t, 2, len(full.WorkTagSexual))
	require.Equal(t, 1, len(full.VocabSexual))
	require.Equal(t, 1, len(full.VocabHidden))
	assert.False(t, full.Truncated)

	limited := BuildPlan(verdicts, nil, testState(), PlanOpts{MinConfidence: 0.90, Limit: 3})
	assert.True(t, limited.Truncated)
	assert.Equal(t, full.WorkTagSexual, limited.WorkTagSexual)
	assert.Equal(t, full.VocabSexual, limited.VocabSexual)
	assert.Empty(t, limited.VocabHidden, "the budget ran out before the last bucket")
	assert.Len(t, limited.Review, 2, "review is never truncated")
}

func TestBuildPlanDedupes(t *testing.T) {
	plan := BuildPlan([]Verdict{
		v("bangumi", "調教", "sexual", 0.99),
		v("bangumi", "調教", "sexual", 0.95),
	}, nil, testState(), PlanOpts{MinConfidence: 0.90})
	assert.Len(t, plan.WorkTagSexual, 1)
	assert.Equal(t, []string{"调教"}, plan.VocabSexual)
}

type fakeWriter struct {
	work   []string
	sexual []string
	hidden []string
	rows   int64
}

func (f *fakeWriter) setWorkTagSexual(_ context.Context, source, name string) (int64, error) {
	f.work = append(f.work, source+":"+name)
	return f.rows, nil
}

func (f *fakeWriter) setTagSexual(_ context.Context, name string) (int64, error) {
	f.sexual = append(f.sexual, name)
	return f.rows, nil
}

func (f *fakeWriter) setTagHidden(_ context.Context, name string) (int64, error) {
	f.hidden = append(f.hidden, name)
	return f.rows, nil
}

func TestExecutePlanDryRunGating(t *testing.T) {
	plan := BuildPlan([]Verdict{
		v("bangumi", "調教", "sexual", 0.99),
		v("dlsite", "視覚小説", "junk", 0.99),
	}, nil, testState(), PlanOpts{MinConfidence: 0.90})

	dry := &fakeWriter{rows: 7}
	st := &ApplyStats{Plan: plan}
	require.NoError(t, executePlan(t.Context(), dry, plan, false, st))
	assert.Empty(t, dry.work)
	assert.Empty(t, dry.sexual)
	assert.Empty(t, dry.hidden)
	assert.Zero(t, st.WorkTagRows)

	run := &fakeWriter{rows: 7}
	st2 := &ApplyStats{Plan: plan}
	require.NoError(t, executePlan(t.Context(), run, plan, true, st2))
	assert.Equal(t, []string{"bangumi:調教"}, run.work)
	assert.Equal(t, []string{"调教"}, run.sexual)
	assert.Equal(t, []string{"视觉小说"}, run.hidden)
	assert.EqualValues(t, 7, st2.WorkTagRows)
	assert.EqualValues(t, 7, st2.VocabSexualRows)
	assert.EqualValues(t, 7, st2.VocabHiddenRows)
	assert.Zero(t, st2.Errors)
}

func TestReadReviewedRejectsBadLines(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad-class.jsonl")
	require.NoError(t, os.WriteFile(bad, []byte(`{"source":"bangumi","name":"x","class":"porn"}`+"\n"), 0o644))
	_, err := readReviewed(bad)
	require.ErrorContains(t, err, "invalid class")

	noName := filepath.Join(dir, "no-name.jsonl")
	require.NoError(t, os.WriteFile(noName, []byte(`{"source":"bangumi","class":"junk"}`+"\n"), 0o644))
	_, err = readReviewed(noName)
	require.ErrorContains(t, err, "needs both source and name")

	ok := filepath.Join(dir, "ok.jsonl")
	require.NoError(t, os.WriteFile(ok, []byte("\n"+`{"source":"bangumi","name":"拔作","class":"sexual"}`+"\n\n"), 0o644))
	lines, err := readReviewed(ok)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, "拔作", lines[0].Name)
}

func TestVerdictSourceKeys(t *testing.T) {
	got := verdictSourceKeys([]Verdict{
		{Source: "dlsite"}, {Source: "bangumi"}, {Source: "bangumi"}, {Source: VocabSource},
	}, []ReviewedLine{{Source: "vndb"}, {Source: VocabSource}})
	assert.Equal(t, []string{"bangumi", "dlsite", "vndb"}, got)
}

func TestApplyRequiresPaths(t *testing.T) {
	_, err := Apply(t.Context(), ApplyOpts{In: "x.jsonl"})
	require.ErrorContains(t, err, "DSN is required")

	_, err = Apply(t.Context(), ApplyOpts{DSN: "postgres://unused"})
	require.ErrorContains(t, err, "verdict JSONL is required")
}

func TestReport(t *testing.T) {
	in := filepath.Join(t.TempDir(), "verdicts.jsonl")
	require.NoError(t, writeVerdicts(in, []Verdict{
		v("bangumi", "拔作", "sexual", 0.97),
		v("bangumi", "調教", "sexual", 0.62),
		v("bangumi", "PC", "junk", 0.99),
		v("bangumi", "校园", "normal", 0.88),
		v("dlsite", "???", "banana", 0.99),
	}))
	st, err := Report(in, 0.90)
	require.NoError(t, err)
	assert.Equal(t, 5, st.Total)
	assert.Equal(t, 1, st.Unknown)
	assert.Equal(t, 2, st.ByClass[ClassSexual])
	assert.Equal(t, 1, st.Confident[ClassSexual])
	assert.Equal(t, 1, st.ByClassBucket[ClassSexual]["0.50-0.70"])
	assert.Equal(t, 4, st.Sources["bangumi"])
	assert.Contains(t, st.String(), "total=5 unknown_class=1")
}
