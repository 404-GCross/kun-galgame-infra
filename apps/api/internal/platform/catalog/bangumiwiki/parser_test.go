package bangumiwiki

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_ScalarFields(t *testing.T) {
	box, err := Parse(`{{Infobox Game
|中文名= 测试游戏
|发行日期= 2020-01-01
}}`)
	require.NoError(t, err)

	assert.Equal(t, "Game", box.Type)
	require.Len(t, box.Fields, 2)
	assert.Equal(t, Field{Key: "中文名", Value: "测试游戏"}, box.Fields[0])
	assert.Equal(t, Field{Key: "发行日期", Value: "2020-01-01"}, box.Fields[1])
}

func TestParse_ArrayFields(t *testing.T) {
	box, err := Parse(`{{Infobox Game
|别名={
[Alias A]
[Alias B]
}
|平台={
[Windows]
}
}}`)
	require.NoError(t, err)

	assert.Equal(t, "Game", box.Type)
	require.Len(t, box.Fields, 2)

	alias := box.Fields[0]
	assert.Equal(t, "别名", alias.Key)
	assert.True(t, alias.Array)
	assert.Equal(t, []Item{{Value: "Alias A"}, {Value: "Alias B"}}, alias.Items)

	platform := box.Fields[1]
	assert.Equal(t, "平台", platform.Key)
	assert.True(t, platform.Array)
	assert.Equal(t, []Item{{Value: "Windows"}}, platform.Items)
}

func TestParse_MalformedInput(t *testing.T) {
	malformed := map[string]string{
		"unclosed array":  "{{Infobox Game\n|别名={\n[Alias A]\n}}",
		"missing suffix":  "{{Infobox Game\n|中文名= 测试游戏\n",
		"missing prefix":  "not an infobox at all",
		"bare array item": "{{Infobox Game\n|别名={\nAlias A\n}\n}}",
	}
	for name, src := range malformed {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				_, err := Parse(src)
				assert.Error(t, err)
			})
		})
	}
}

func TestParse_EmptyInput(t *testing.T) {
	for _, src := range []string{"", "  \n\t\n"} {
		box, err := Parse(src)
		require.NoError(t, err)
		assert.Equal(t, Infobox{}, box)
	}
}
