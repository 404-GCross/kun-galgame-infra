package bgmsummaries

import (
	"strings"
	"testing"
)

// TestDetectLang pins the wave-166 classifier: hiragana-against-han share, with
// a katakana-only exception and a no-CJK abstention. The emitted codes must be
// EXACTLY the read face's ("ja" / "zh-Hans").
func TestDetectLang(t *testing.T) {
	cases := []struct {
		name, in, want string
		wantOK         bool
	}{
		{"pure hiragana", "ふたりの少女が出会う物語。", "ja", true},
		{"japanese prose", "この春に入学し、ルームメイトとなった三人の物語。", "ja", true},
		{"katakana only is japanese", "サンプルゲームノアラスジ", "ja", true},
		{"simplified chinese", "两位新人冒险者发现自己被困孤岛。", "zh-Hans", true},
		{"traditional chinese labeled zh-Hans (v1 trade-off)", "隸屬於王國魔法部隊的年輕魔法使。", "zh-Hans", true},

		// The defect wave 166 fixes: Chinese prose quoting Japanese proper
		// nouns. Under the old "any kana ⇒ ja" rule every one of these was
		// filed Japanese — ~3.5k rows in prod.
		{"chinese quoting a kana title", "中文简介，但引用了「ドキドキ」这个词。", "zh-Hans", true},
		{"chinese with a katakana series name", "钢铁巨人ヤルセナイザー在完成任务后的归程中坠落在地球上。", "zh-Hans", true},
		{
			"chinese blurb with parenthesised furigana (real prod shape)",
			"百花凌乱，少女起舞。六位大和抚子和你的美妙邂逅。主人公所住的御前（みさき）市在战国乱世的同时，" +
				"也流传着英勇的女性守护着国家的佳话。武道优秀的女性们聚集于此，为了守护故乡而战，" +
				"而主人公则在她们之间寻找自己的位置，逐渐明白了何为真正的守护。",
			"zh-Hans", true,
		},

		// Kanji-only text has zero hiragana, so the share is 0 → Chinese. That
		// is the right call: Japanese prose cannot be kanji-only.
		{"kanji only reads as zh", "日本語漢字文", "zh-Hans", true},

		// No CJK evidence at all — abstain rather than guess a tag (wave 164).
		{"empty", "", "", false},
		{"latin only", "A doujin RPG.", "", false},
		{"korean", "왕국 마법 부대에 소속된 젊은 마법사.", "", false},
		{"halfwidth katakana is NOT in the range", "ｹﾞｰﾑ", "", false},
	}
	for _, c := range cases {
		got, ok := detectLang(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: detectLang(%q) = (%q, %v), want (%q, %v)", c.name, c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// TestDetectLangThresholdEdges pins the behaviour AT the 0.05 boundary rather
// than only far from it, so a future tweak to hiraganaShare fails loudly.
func TestDetectLangThresholdEdges(t *testing.T) {
	han := strings.Repeat("汉", 100)
	if c := countScripts(han); c.han != 100 || c.hira != 0 {
		t.Fatalf("fixture drifted: %+v (want han=100 hira=0)", c)
	}
	// 5 hiragana against 100 han = 0.0476 < 0.05 → Chinese.
	if got, ok := detectLang(han + "あいうえお"); got != langZhHans || !ok {
		t.Errorf("just below the threshold must be zh-Hans, got %q/%v", got, ok)
	}
	// 6 hiragana against 100 han = 0.0566 >= 0.05 → Japanese. Erring to ja is
	// the safe side: it costs a redundant translation pass, while a wrong
	// zh-Hans would show Japanese under the reader's Chinese tab AND suppress
	// the machine-translation lane.
	if got, ok := detectLang(han + "あいうえおか"); got != langJa || !ok {
		t.Errorf("just above the threshold must be ja, got %q/%v", got, ok)
	}
}

// TestDetectLangShortTextLimitation records — rather than hides — the one place
// the classifier is knowingly wrong: a SHORT Chinese blurb whose few characters
// include a parenthesised furigana reads above the threshold and is filed ja.
//
// This is left uncorrected on purpose. The ratio carries no signal at this
// length, and the mistake falls on the cheap side: the text goes out under the
// Japanese tab and the machine lane then produces a zh-Hans row from it. The
// expensive mistake — Japanese filed as Chinese — needs the threshold to stay
// where it is. If this ever needs fixing, the fix is an LLM adjudication pass
// over the gray band (the wave-87/164 pattern), not a moved threshold.
func TestDetectLangShortTextLimitation(t *testing.T) {
	got, ok := detectLang("缘之空的FD。新增班长线和穹妹后日谈（そらのおと）。")
	if !ok || got != langJa {
		t.Errorf("documented limitation changed: got %q/%v, expected the ja mis-file", got, ok)
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

// TestSitePredicate pins the population gate — in particular that "claimed" is
// a PROPERTY test and never a site literal. Wave 161 renamed the only value
// that has ever existed (galgame_wiki → kungal), and every lane that had pinned
// the literal went silently empty.
func TestSitePredicate(t *testing.T) {
	for _, c := range []struct{ pop, want string }{
		{PopulationAll, "TRUE"},
		{"", "TRUE"},
		{PopulationBodyless, "(w.site IS NULL OR w.site = '')"},
		{PopulationClaimed, "(w.site IS NOT NULL AND w.site <> '')"},
	} {
		got, err := sitePredicate(c.pop)
		if err != nil || got != c.want {
			t.Errorf("sitePredicate(%q) = %q, %v; want %q, nil", c.pop, got, err, c.want)
		}
	}
	if _, err := sitePredicate("kungal"); err == nil {
		t.Error("a site VALUE is not a population name — must be rejected, not silently accepted")
	}
}
