package entityintros

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectLang(t *testing.T) {
	assert.Equal(t, "ja", detectLang("ふたりの冒険者が孤島に流れ着く。"), "hiragana")
	assert.Equal(t, "ja", detectLang("汉字とカタカナ混在テキスト"), "katakana wins over han")
	assert.Equal(t, "zh-Hans", detectLang("两位新人冒险者发现自己被困孤岛。"), "no kana")
	assert.Equal(t, "zh-Hans", detectLang("Latin only text"), "heuristic floor: non-kana defaults zh-Hans")
}

func TestStripVNDBMarkupSpoiler(t *testing.T) {
	in := "Visible intro.\n\n[spoiler]She is secretly the villain.\nSecond spoiler line.[/spoiler]\n\nVisible tail."
	out, stripped := stripVNDBMarkup(in)
	assert.True(t, stripped)
	assert.Equal(t, "Visible intro.\n\nVisible tail.", out, "span removed, blank-line residue collapsed")
	assert.NotContains(t, out, "villain")

	in = "A [SPOILER]x[/SPOILER] B [spoiler]y[/spoiler] C"
	out, stripped = stripVNDBMarkup(in)
	assert.True(t, stripped)
	assert.Equal(t, "A  B  C", out)

	in = "Safe part. [spoiler]The twist is"
	out, stripped = stripVNDBMarkup(in)
	assert.True(t, stripped)
	assert.Equal(t, "Safe part.", out)
	assert.NotContains(t, out, "twist")
}

func TestStripVNDBMarkupLightTags(t *testing.T) {
	in := "She is [b]bold[/b] and [i]witty[/i].\n\n[From [url=http://en.wikipedia.org/wiki/X]Wikipedia[/url]]"
	out, stripped := stripVNDBMarkup(in)
	assert.False(t, stripped, "no spoiler span here")
	assert.Equal(t, "She is bold and witty.\n\n[From Wikipedia]", out)

	out, stripped = stripVNDBMarkup("Plain description.")
	assert.False(t, stripped)
	assert.Equal(t, "Plain description.", out)
}

func TestStripVNDBMarkupEmptied(t *testing.T) {
	out, stripped := stripVNDBMarkup("[spoiler]everything is a spoiler[/spoiler]")
	assert.True(t, stripped)
	assert.Equal(t, "", out)
}

func TestNormalizeText(t *testing.T) {
	assert.Equal(t, "line one\nline two", normalizeText("line one\r\nline two"), "CRLF → LF")
	assert.Equal(t, "already fine\n", normalizeText("already fine\n"))
}
