package tagsafety

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelectTodoResumeSkip is the resume contract: names already present in the
// output JSONL are skipped (never re-billed to the gateway), the rest keep pool
// order, and --limit takes a deterministic PREFIX of what remains so successive
// limited runs walk the vocabulary instead of re-judging its head.
func TestSelectTodoResumeSkip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "verdicts.jsonl")
	require.NoError(t, writeVerdicts(out, []Verdict{
		{Source: "bangumi", Name: "拔作", Class: "sexual", Confidence: 0.97},
		{Source: "bangumi", Name: "PC", Class: "junk", Confidence: 0.99},
		// same NAME on a different source is a different record — must NOT skip.
		{Source: "dlsite", Name: "調教", Class: "sexual", Confidence: 0.95},
	}))

	done, err := loadDone(out)
	require.NoError(t, err)
	require.Len(t, done, 3)

	pool := []NameInput{
		{Source: "bangumi", Name: "拔作"},
		{Source: "bangumi", Name: "PC"},
		{Source: "bangumi", Name: "校园"},
		{Source: "bangumi", Name: "青梅竹马"},
		{Source: "dlsite", Name: "調教"},
		{Source: "dlsite", Name: "ファンタジー"},
	}

	st := &ClassifyStats{ClassCounts: map[Class]int{}}
	todo := selectTodo(pool, done, 0, st)
	assert.Equal(t, 3, st.Skipped)
	require.Len(t, todo, 3)
	assert.Equal(t, "校园", todo[0].Name)
	assert.Equal(t, "青梅竹马", todo[1].Name)
	assert.Equal(t, "ファンタジー", todo[2].Name)

	// --limit caps the NEW names only.
	st2 := &ClassifyStats{ClassCounts: map[Class]int{}}
	limited := selectTodo(pool, done, 2, st2)
	require.Len(t, limited, 2)
	assert.Equal(t, "校园", limited[0].Name)
	assert.Equal(t, "青梅竹马", limited[1].Name)
}

// TestLoadDoneMissingFile: a fresh run (no output file yet) is the empty set,
// not an error — but a corrupt file IS an error, because silently restarting
// from zero would double-bill the gateway and duplicate every line.
func TestLoadDoneMissingFile(t *testing.T) {
	done, err := loadDone(filepath.Join(t.TempDir(), "nope.jsonl"))
	require.NoError(t, err)
	assert.Empty(t, done)

	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	require.NoError(t, os.WriteFile(bad, []byte("{not json}\n"), 0o644))
	_, err = loadDone(bad)
	require.Error(t, err)
}

// TestAppenderResumesAcrossRuns: appending (never rewriting) is what makes an
// interrupted run resumable — the second run's lines land after the first's and
// the whole file still parses.
func TestAppenderResumesAcrossRuns(t *testing.T) {
	out := filepath.Join(t.TempDir(), "verdicts.jsonl")

	a1, err := newVerdictAppender(out)
	require.NoError(t, err)
	require.NoError(t, a1.append(Verdict{Source: "bangumi", Name: "拔作", Class: "sexual", Confidence: 0.97}))
	require.NoError(t, a1.Close())

	a2, err := newVerdictAppender(out)
	require.NoError(t, err)
	require.NoError(t, a2.append(Verdict{Source: "bangumi", Name: "校园", Class: "normal", Confidence: 0.9}))
	require.NoError(t, a2.Close())

	recs, err := readVerdicts(out)
	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "拔作", recs[0].Name)
	assert.Equal(t, "校园", recs[1].Name)
}

// TestBatchesNeverMixSources: the prompt states which source it is judging, so a
// batch must not straddle two of them; batches are otherwise exactly BatchSize.
func TestBatchesNeverMixSources(t *testing.T) {
	todo := []NameInput{
		{Source: "bangumi", Name: "a"}, {Source: "bangumi", Name: "b"}, {Source: "bangumi", Name: "c"},
		{Source: "dlsite", Name: "d"}, {Source: "dlsite", Name: "e"},
		{Source: VocabSource, Name: "f"},
	}
	got := batches(todo, 2)
	require.Len(t, got, 4)
	assert.Equal(t, []string{"a", "b"}, names(got[0]))
	assert.Equal(t, []string{"c"}, names(got[1]), "source boundary cuts the batch short")
	assert.Equal(t, []string{"d", "e"}, names(got[2]))
	assert.Equal(t, []string{"f"}, names(got[3]))
	for _, b := range got {
		for _, n := range b {
			assert.Equal(t, b[0].Source, n.Source)
		}
	}
}

func names(in []NameInput) []string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		out = append(out, n.Name)
	}
	return out
}

// TestClassifyRequiresSeamAndPaths: the guardrails fire before any DB handle is
// opened, so a mis-typed invocation cannot reach a live database.
func TestClassifyRequiresSeamAndPaths(t *testing.T) {
	_, err := Classify(t.Context(), MockClassifier{}, ClassifyOpts{Out: "x.jsonl"})
	require.ErrorContains(t, err, "DSN is required")

	_, err = Classify(t.Context(), MockClassifier{}, ClassifyOpts{DSN: "postgres://unused"})
	require.ErrorContains(t, err, "output path is required")

	_, err = Classify(t.Context(), nil, ClassifyOpts{DSN: "postgres://unused", Out: "x.jsonl"})
	require.ErrorContains(t, err, "classifier is required")
}
