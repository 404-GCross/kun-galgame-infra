package tagsafety

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func inputs(source string, names ...string) []NameInput {
	out := make([]NameInput, 0, len(names))
	for i, n := range names {
		out = append(out, NameInput{Source: source, Name: n, Uses: 100 - i, Votes: 500 - i})
	}
	return out
}

func TestRenderBatchPrompt(t *testing.T) {
	got := renderBatchPrompt(inputs("bangumi", "拔作", "纯爱"))
	assert.Contains(t, got, "数据源:bangumi")
	assert.Contains(t, got, "1. 拔作(用量 100/500)")
	assert.Contains(t, got, "2. 纯爱(用量 99/499)")
	assert.Contains(t, got, "共 2 条")
}

func TestParseBatchRoundTrip(t *testing.T) {
	in := inputs("bangumi", "拔作", "纯爱", "PC")
	reply := "```json\n" + `{"results":[
		{"index":1,"name":"拔作","class":"sexual","confidence":0.97,"reason":"成人向"},
		{"index":2,"name":"纯爱","class":"NORMAL","confidence":0.93,"reason":"恋爱不算色情"},
		{"index":3,"name":"PC","class":"junk","confidence":0.99,"reason":"平台词"}
	]}` + "\n```"

	got, err := parseBatch(reply, in)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, ClassSexual, got[0].Class)
	assert.Equal(t, "拔作", got[0].Name)
	assert.InDelta(t, 0.97, got[0].Confidence, 1e-9)
	assert.Equal(t, ClassNormal, got[1].Class, "class is case-insensitive")
	assert.Equal(t, ClassJunk, got[2].Class)
	assert.Equal(t, "平台词", got[2].Reason)
}

func TestParseBatchAlignmentGuards(t *testing.T) {
	in := inputs("bangumi", "拔作", "纯爱", "PC", "触手")
	reply := `{"results":[
		{"index":1,"name":"拔作","class":"sexual","confidence":0.97,"reason":"ok"},
		{"index":1,"name":"拔作","class":"normal","confidence":0.10,"reason":"duplicate index — ignored"},
		{"index":2,"name":"純愛","class":"normal","confidence":0.9,"reason":"echo mismatch"},
		{"index":3,"name":"PC","class":"platform","confidence":0.9,"reason":"invalid class"},
		{"index":9,"name":"触手","class":"sexual","confidence":0.9,"reason":"index out of range"}
	]}`

	got, err := parseBatch(reply, in)
	require.NoError(t, err)
	require.Len(t, got, 4)
	assert.Equal(t, ClassSexual, got[0].Class, "first verdict for an index wins")
	assert.Equal(t, "ok", got[0].Reason)
	assert.Empty(t, got[1].Class, "name echo mismatch must not be guessed at")
	assert.Empty(t, got[2].Class, "invalid class is dropped, never coerced")
	assert.Empty(t, got[3].Class, "out-of-range index cannot land anywhere")
}

func TestParseBatchUnusable(t *testing.T) {
	in := inputs("bangumi", "拔作")

	_, err := parseBatch(`{"results":[{"index":5,"name":"x","class":"junk"}]}`, in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aligned zero")

	_, err = parseBatch("I'm sorry, I can't do that.", in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode batch reply")
}

func TestMockClassifier(t *testing.T) {
	in := inputs("bangumi", "PC", "Galgame", "NTR", "调教", "校园", "汉化")
	got, model, err := MockClassifier{Model: "glm-5.2"}.ClassifyBatch(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, got, len(in))
	assert.Equal(t, "mock:glm-5.2", model)
	assert.True(t, strings.HasPrefix(model, "mock:"))

	assert.Equal(t, ClassJunk, got[0].Class, "PC = platform word")
	assert.Equal(t, ClassJunk, got[1].Class, "Galgame = site-wide truism")
	assert.Equal(t, ClassSexual, got[2].Class)
	assert.Equal(t, ClassSexual, got[3].Class)
	assert.Equal(t, ClassNormal, got[4].Class)
	assert.Equal(t, ClassNormal, got[5].Class, "汉化 is meta, NOT junk — the prompt's pinned counter-example")
}

func TestClassifySystemPromptPins(t *testing.T) {
	for _, must := range []string{"纯爱", "全年龄", "汉化", "乙女", "kind=meta", `"results"`, "class"} {
		assert.Contains(t, ClassifySystemPrompt, must)
	}
}

func TestStripFence(t *testing.T) {
	assert.Equal(t, `{"a":1}`, stripFence("```json\n{\"a\":1}\n```"))
	assert.Equal(t, `{"a":1}`, stripFence("```\n{\"a\":1}\n```"))
	assert.Equal(t, `{"a":1}`, stripFence(` {"a":1} `))
}
