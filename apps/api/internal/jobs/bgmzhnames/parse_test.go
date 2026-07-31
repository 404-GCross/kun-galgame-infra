package bgmzhnames

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseFieldsGuard pins the dirty-value guard: only a real Fields ARRAY is
// readable. The scalar case is not hypothetical — src_bangumi carries such rows
// (the step-81 charattrs finding).
func TestParseFieldsGuard(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":          ``,
		"json null":      `null`,
		"no fields key":  `{"Type":"Crt"}`,
		"fields null":    `{"Fields":null}`,
		"fields scalar":  `{"Fields":"简体中文名"}`,
		"fields object":  `{"Fields":{"Key":"简体中文名"}}`,
		"fields number":  `{"Fields":42}`,
		"malformed json": `{"Fields":[`,
	} {
		t.Run(name, func(t *testing.T) {
			_, ok := parseFields([]byte(raw))
			assert.False(t, ok, "unreadable infobox must be refused, not guessed")
		})
	}

	fields, ok := parseFields([]byte(`{"Fields":[{"Key":"简体中文名","Value":"鲁路修·兰佩路基","Items":null}]}`))
	require.True(t, ok)
	require.Len(t, fields, 1)
	assert.Equal(t, "鲁路修·兰佩路基", fields[0].Value)
}

// TestIsChineseName pins the sorting heuristic: Han required, kana vetoed, and
// the Katakana-block punctuation that is script Common must NOT read as kana.
func TestIsChineseName(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"simplified", "鲁路修·兰佩路基", true},
		{"traditional", "魯路修‧蘭佩路基", true},
		{"katakana middle dot is punctuation", "艾尔・罗莱特", true},
		{"han with latin", "EVA 初号机", true},
		{"hiragana", "さくら", false},
		{"katakana", "ルルーシュ・ランペルージ", false},
		{"kanji plus kana", "涼宮ハルヒ", false},
		{"latin only", "Cock Robin", false},
		{"sentinel", "？？？", false},
		{"digits", "PG-7", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isChineseName(tc.in))
		})
	}
}

// TestProjectNames drives the real Lelouch infobox (bangumi character 1): the
// main name leads, the Chinese-declaring alias item follows, and every
// non-Chinese sibling — English, Japanese, and the UNTAGGED items — is refused.
func TestProjectNames(t *testing.T) {
	const lelouch = `{"Type":"Crt","Fields":[
		{"Key":"简体中文名","Value":"鲁路修·兰佩路基","Items":null},
		{"Key":"别名","Value":"","Items":[
			{"Key":"","Value":"L.L."},
			{"Key":"","Value":"勒鲁什"},
			{"Key":"","Value":"鲁鲁修"},
			{"Key":"","Value":"ゼロ"},
			{"Key":"英文名","Value":"Lelouch Lamperouge"},
			{"Key":"第二中文名","Value":"鲁路修·冯·布里塔尼亚"},
			{"Key":"日文名","Value":"ルルーシュ・ヴィ・ブリタニア"},
			{"Key":"纯假名","Value":""},
			{"Key":"罗马字","Value":""}]},
		{"Key":"性别","Value":"男","Items":null}]}`

	fields, ok := parseFields([]byte(lelouch))
	require.True(t, ok)
	names, rejected := projectNames(fields)
	assert.Equal(t, []string{"鲁路修·兰佩路基", "鲁路修·冯·布里塔尼亚"}, names,
		"main name first, then the Chinese-declaring alias item; untagged items are not collected")
	assert.Zero(t, rejected, "the refused items are refused by KEY, not by the Chinese test")
}

// TestProjectNamesSorting covers the remaining decisions in one infobox: the
// Chinese test rejecting a declared-Chinese value, a Japanese-kanji 日文名 that
// the key filter must stop (the heuristic alone could not), whitespace
// trimming, and de-duplication inside one character.
func TestProjectNamesSorting(t *testing.T) {
	const raw = `{"Fields":[
		{"Key":"简体中文名","Value":"  夏娜  ","Items":null},
		{"Key":"别名","Value":"","Items":[
			{"Key":"第二中文名","Value":"夏娜"},
			{"Key":"第三中文名","Value":"炎发灼眼的杀手"},
			{"Key":"中文名","Value":"Shana"},
			{"Key":"日文名","Value":"渡辺汐里"},
			{"Key":"繁体中文名","Value":"夏娜"},
			{"Key":"第四中文名","Value":"   "}]}]}`

	fields, ok := parseFields([]byte(raw))
	require.True(t, ok)
	names, rejected := projectNames(fields)
	assert.Equal(t, []string{"夏娜", "炎发灼眼的杀手"}, names,
		"trimmed, deduplicated, and the Latin value dropped")
	assert.Equal(t, 1, rejected, "only the Latin 中文名 reaches the Chinese test and fails it")
	assert.NotContains(t, names, "渡辺汐里", "a Japanese-kanji 日文名 is stopped by the key filter")
}

// TestProjectNamesNoSupply pins the empty case: an infobox with only
// non-Chinese name fields yields nothing at all.
func TestProjectNamesNoSupply(t *testing.T) {
	fields, ok := parseFields([]byte(`{"Fields":[
		{"Key":"别名","Value":"","Items":[{"Key":"日文名","Value":"ルルーシュ"},{"Key":"罗马字","Value":"Lelouch"}]},
		{"Key":"性别","Value":"男","Items":null}]}`))
	require.True(t, ok)
	names, rejected := projectNames(fields)
	assert.Empty(t, names)
	assert.Zero(t, rejected)
}
