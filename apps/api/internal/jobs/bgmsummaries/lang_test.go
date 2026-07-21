package bgmsummaries

import "testing"

// TestDetectLang pins the spec heuristic: any hiragana/katakana → ja, else
// zh-Hans — and that the emitted codes are EXACTLY the read-face pivot's.
func TestDetectLang(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"pure hiragana", "ふたりの少女が出会う物語。", "ja"},
		{"pure katakana", "サンプルゲームノアラスジ", "ja"},
		{"kanji only reads as zh", "日本語漢字文", "zh-Hans"}, // no kana → the heuristic's known edge
		{"simplified chinese", "两位新人冒险者发现自己被困孤岛。", "zh-Hans"},
		{"traditional chinese labeled zh-Hans (v1 trade-off)", "隸屬於王國魔法部隊的年輕魔法使。", "zh-Hans"},
		{"mixed zh with one kana wins ja", "中文简介，但引用了「ドキドキ」这个词。", "ja"},
		{"empty", "", "zh-Hans"},
		{"latin only", "A doujin RPG.", "zh-Hans"},
		{"halfwidth katakana is NOT in the range", "ｹﾞｰﾑ", "zh-Hans"},
	}
	for _, c := range cases {
		if got := detectLang(c.in); got != c.want {
			t.Errorf("%s: detectLang(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestNormalizeSummary pins CRLF→LF as the ONLY transformation (text otherwise
// verbatim; the dump has no bare \r, so none is handled).
func TestNormalizeSummary(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"crlf collapses", "第一行\r\n第二行\r\n", "第一行\n第二行\n"},
		{"lf untouched", "第一行\n第二行", "第一行\n第二行"},
		{"everything else verbatim", "  空白　と\t全角  ", "  空白　と\t全角  "},
	}
	for _, c := range cases {
		if got := normalizeSummary(c.in); got != c.want {
			t.Errorf("%s: normalizeSummary(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestIsBodyless pins the XOR predicate.
func TestIsBodyless(t *testing.T) {
	empty, claimed := "", "galgame_wiki"
	if !isBodyless(nil) || !isBodyless(&empty) {
		t.Error("nil/empty site must be bodyless")
	}
	if isBodyless(&claimed) {
		t.Error("claimed site must not be bodyless")
	}
}
