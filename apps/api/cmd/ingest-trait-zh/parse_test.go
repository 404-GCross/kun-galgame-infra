package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// miniScript reproduces every structural shape of the real userscript that the
// parser has to survive, in ~60 lines: a decoy rule before the one we want, the
// unlabelled hand-curated head section, a VNDB-character-page 精翻 section, a
// VN-TAG section (excluded) whose tail turns into traits at a //特征
// sub-heading, the 人物特征 section, an unlabelled trailing section (excluded),
// quality markers, a commented-out entry, a trailing line comment, and a
// duplicate key.
const miniScript = `
let mainMap = {
    "Ahoge": "不是特征页的呆毛",
};
let rules = [
    {
        name:'(用户页)=>用户主页',
        map:{
            "Decoy": "诱饵",
        },
        titleMap:{},
    },
    {
        name:'(标签与特征)=>特征页|标签页|人物页',
        regular:/^\/(i|g)/i,
        map:{
            /*大类*/
            "Hair":"毛发",
            "Voiced by": "声优 ",/*添加间隔*/
            "Ahoge": "呆毛(アホげ)",
            /*todo ----精翻,来源:https://vndb.org/v12849/chars#chars*/
            "Coodere": "冷娇(クウデレ)",
            "Modern Tsundere": "现代傲娇'",
            "Rough Draft": "粗翻°",
            //"Cosplay": "角色扮演",
            /*todo ----暂未校对,VN标签,翻译贡献者:https://greasyfork.org/zh-CN/users/1210764-railguns*/
            //标签
            "Protagonist's Bedroom": "主角的卧室",
            "Rape": "标签义的强奸",
            //特征
            "Albino": "白化病",
            /*todo  ----暂未校对,人物特征,翻译贡献者:https://greasyfork.org/zh-CN/users/1210764-railguns*/
            //物品
            "Quiver": "箭袋",
            "Ahoge": "重复键,先到先得",
            /*todo  ----暂未校对,翻译贡献者:https://greasyfork.org/zh-CN/users/1210764-railguns*/
            "Amputee Hero": "截肢男主角",
        },
        titleMap:{},
        specialMap:{},
    },
    {
        name:'(通用项)=>作品详情页',
        map: {
            "Rape": "另一个规则里的强奸",
        },
    },
];
`

func writeMini(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mini.js")
	require.NoError(t, os.WriteFile(p, []byte(miniScript), 0o600))
	return p
}

func asMap(pairs []pair) map[string]string {
	m := map[string]string{}
	for _, p := range pairs {
		if _, ok := m[p.En]; !ok {
			m[p.En] = p.Zh
		}
	}
	return m
}

func TestParseScriptReadsOnlyTheTraitSections(t *testing.T) {
	got := asMap(mustParse(t, writeMini(t), parseOpts{}))

	assert.Equal(t, map[string]string{
		// hand-curated head section
		"Hair":      "毛发",
		"Voiced by": "声优",
		"Ahoge":     "呆毛(アホげ)",
		// chars#chars 精翻 section, quality markers stripped
		"Coodere":         "冷娇(クウデレ)",
		"Modern Tsundere": "现代傲娇",
		"Rough Draft":     "粗翻",
		// the //特征 tail of the VN-TAG section
		"Albino": "白化病",
		// 人物特征 section
		"Quiver": "箭袋",
	}, got)
}

func TestParseScriptExcludesForeignVocabulary(t *testing.T) {
	got := asMap(mustParse(t, writeMini(t), parseOpts{}))

	for _, excluded := range []string{
		"Decoy",                 // another rule entirely
		"Protagonist's Bedroom", // VN-TAG section, before the //特征 tail
		"Rape",                  // ditto — a tag sense, not the trait sense
		"Amputee Hero",          // trailing unlabelled section
		"Cosplay",               // commented-out entry
	} {
		assert.NotContains(t, got, excluded, "%q must not be ingested", excluded)
	}
}

func TestParseScriptIncludeTagVocabOptIn(t *testing.T) {
	got := asMap(mustParse(t, writeMini(t), parseOpts{IncludeTagVocab: true}))

	assert.Equal(t, "标签义的强奸", got["Rape"], "the VN-tag section is read only when explicitly asked for")
	assert.Equal(t, "主角的卧室", got["Protagonist's Bedroom"])
	assert.NotContains(t, got, "Amputee Hero", "the unlabelled trailing section stays out either way")
	assert.NotContains(t, got, "Decoy")
}

func TestParseScriptFirstWritingOfAKeyWins(t *testing.T) {
	pairs := mustParse(t, writeMini(t), parseOpts{})
	n := 0
	for _, p := range pairs {
		if p.En == "Ahoge" {
			n++
		}
	}
	assert.Equal(t, 1, n)
	assert.Equal(t, "呆毛(アホげ)", asMap(pairs)["Ahoge"])
}

func TestParseScriptFailsLoudlyWhenTheLayoutMoved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "other.js")
	require.NoError(t, os.WriteFile(p, []byte("let mainMap = { \"Home\": \"首页\" };\n"), 0o600))

	_, err := parseScript(p, parseOpts{})
	require.Error(t, err, "a script without the trait rule must not silently ingest nothing")
}

func TestStripQualityMarkers(t *testing.T) {
	assert.Equal(t, "现代傲娇", stripQualityMarkers("现代傲娇'"))
	assert.Equal(t, "粗翻", stripQualityMarkers("粗翻°"))
	assert.Equal(t, "两个都有", stripQualityMarkers("两个都有°' "))
	assert.Equal(t, "呆毛(アホげ)", stripQualityMarkers("呆毛(アホげ)"))
}

func mustParse(t *testing.T, path string, opts parseOpts) []pair {
	t.Helper()
	pairs, err := parseScript(path, opts)
	require.NoError(t, err)
	return pairs
}
