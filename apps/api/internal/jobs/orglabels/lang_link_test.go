package orglabels

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://www.alicesoft.com/", "www.alicesoft.com", true},
		{"http://www.illusion.jp/", "www.illusion.jp", true},
		{"https://www.eukleia.co.jp/eushully/", "www.eukleia.co.jp/eushully", true},
		{"https://web.archive.org/web/20130208192839/http://www.ivory.co.jp/", "www.ivory.co.jp", true},
		{"http://a.com", "a.com", true},   // dedup-equal to below
		{"https://a.com/", "a.com", true}, // …same normalized id
		{"", "", false},
		{"notaurl", "", false},        // no dot
		{"has space.com", "", false},  // whitespace rejected
	}
	for _, c := range cases {
		got, ok := normalizeURL(c.in)
		assert.Equal(t, c.ok, ok, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}

func TestNormalizeTwitter(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"alice_soft", "alice_soft", true},
		{"@Foo", "foo", true},
		{"https://twitter.com/Bar/", "bar", true},
		{"https://x.com/baz?s=21", "baz", true},
		{"https://mobile.twitter.com/Qux", "qux", true},
		{"", "", false},
		{"has space", "", false},
		{"a/b", "a", true},
	}
	for _, c := range cases {
		got, ok := normalizeTwitter(c.in)
		assert.Equal(t, c.ok, ok, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}

func TestNormalizeCien(t *testing.T) {
	got, ok := normalizeCien("27405")
	assert.True(t, ok)
	assert.Equal(t, "27405", got)
	_, ok = normalizeCien("")
	assert.False(t, ok)
	_, ok = normalizeCien("abc")
	assert.False(t, ok)
}

func TestClassifyBGMKey(t *testing.T) {
	assert.Equal(t, bgmKeyWebsite, classifyBGMKey("官网"))
	assert.Equal(t, bgmKeyWebsite, classifyBGMKey("官方网站"))
	assert.Equal(t, bgmKeyWebsite, classifyBGMKey("Website"))
	assert.Equal(t, bgmKeyWebsite, classifyBGMKey("主页"))
	assert.Equal(t, bgmKeyTwitter, classifyBGMKey("Twitter"))
	assert.Equal(t, bgmKeyTwitter, classifyBGMKey("X (Twitter)"))
	assert.Equal(t, bgmKeyNone, classifyBGMKey("官方微博")) // weibo excluded
	assert.Equal(t, bgmKeyNone, classifyBGMKey("DLsite官网")) // store excluded
	assert.Equal(t, bgmKeyNone, classifyBGMKey("Steam主页"))  // store excluded
	assert.Equal(t, bgmKeyNone, classifyBGMKey("别名"))
}

func TestDetectLang(t *testing.T) {
	assert.Equal(t, langJa, detectLang("テスト"))
	assert.Equal(t, langZhHans, detectLang("测试内容"))
}

func TestDetectLangVNDB(t *testing.T) {
	assert.Equal(t, langEn, detectLangVNDB("age is a Japanese developer"))
	assert.Equal(t, langEn, detectLangVNDB("TYPE-MOON"))
	assert.Equal(t, langJa, detectLangVNDB("アージュは開発会社"))
	assert.Equal(t, langZhHans, detectLangVNDB("这是一个中文说明"))
}

func TestStripVNDBMarkup(t *testing.T) {
	assert.Equal(t, "visible", stripVNDBMarkup("[spoiler]secret[/spoiler]visible"))
	assert.Equal(t, "link", stripVNDBMarkup("[url=http://x]link[/url]"))
	assert.Equal(t, "bold", stripVNDBMarkup("[b]bold[/b]"))
	assert.Equal(t, "a\n\nb", stripVNDBMarkup("a\n\n\n\nb"))
	assert.Equal(t, "before", stripVNDBMarkup("before[spoiler]leak to end"))
}
