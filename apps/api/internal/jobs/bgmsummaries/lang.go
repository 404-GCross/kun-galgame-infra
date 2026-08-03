package bgmsummaries

import "strings"

// Language codes — the SHORT BCP-47 vocabulary the read face merges on
// ("ja"/"en"/"zh-Hans"/"zh-Hant"). Every lane writing catalog_work_intro MUST
// share one lang vocabulary or the consumer's per-language selection mis-fires
// — the 55/56 lesson.
const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
)

// hiraganaShare is the hiragana-against-han ratio below which a summary is
// Simplified Chinese rather than Japanese. See detectLang for where the number
// comes from and why it sits where it does.
const hiraganaShare = 0.05

// normalizeSummary makes the dump text storable verbatim except for line
// endings: CRLF→LF (the Bangumi dump carries \r\n artifacts — 59,819 of the
// type=4 summaries; verified that every \r is part of a \r\n pair, so no bare
// \r handling is needed). No other cleaning, same as steps 52/55.
func normalizeSummary(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// scripts counts the three scripts that decide a Bangumi summary's language.
// Latin, digits and punctuation are deliberately not counted: they carry no
// signal and appear in both languages.
type scripts struct{ hira, kata, han int }

func countScripts(s string) scripts {
	var c scripts
	for _, r := range s {
		switch {
		case r >= 'ぁ' && r <= 'ん':
			c.hira++
		case r >= 'ァ' && r <= 'ヶ':
			c.kata++
		case r >= '一' && r <= '鿿':
			c.han++
		}
	}
	return c
}

// detectLang classifies one summary. ok=false means the text evidences NO
// language and the caller must write nothing.
//
// Bangumi is a CHINESE site carrying both original Japanese blurbs and
// user-written Chinese ones, so the language has to be read off the text. The
// pre-166 rule — "contains any kana ⇒ ja" — mis-filed every Chinese summary
// that quotes a Japanese title or a character's furigana, which is most of
// them: measured over the 28,644 bgm-anchored summaries in prod it labelled
// ~3.5k Chinese texts Japanese.
//
// The discriminator is HIRAGANA against HAN — not "any kana", and not kana
// against total length:
//
//   - Chinese prose quoting Japanese proper nouns carries katakana and kanji
//     but almost no hiragana; hiragana only leaks in as parenthesised furigana.
//     Measured share: < 0.05.
//   - Japanese prose cannot avoid hiragana — it is the particle and inflection
//     script. Measured share: > 0.4 for the whole Japanese mass; the few
//     Japanese texts that dip lower are katakana-heavy release notes and title
//     lists, and none observed below 0.15.
//
// So 0.05 sits in a deep, empirically verified valley (prod distribution: 3,543
// rows below 0.05, then 90 / 53 / 33 across the next three 0.05-wide buckets,
// then the Japanese mass). The ten rows immediately below the line were read by
// hand — all Chinese.
//
// The threshold sits at the CHINESE end of that valley rather than its middle,
// because the two mistakes are not symmetric:
//
//   - Japanese filed as zh-Hans shows Japanese under the reader's Chinese tab
//     AND suppresses the machine-translation lane (intro-mt fills a MISSING
//     zh-Hans, it never replaces one). Unrecoverable without a repair wave.
//   - Chinese filed as ja costs one redundant translation pass and puts Chinese
//     under the Japanese tab. Cheap, and self-correcting.
//
// A text with neither han nor hiragana evidences no CJK at all: of the 927 such
// rows in prod, 741 are plain English and 6 Korean. Those return ok=false —
// filing CJK-free text under a zh tag is precisely the defect wave 164 fixed.
// Katakana-only text is the one exception: it IS Japanese (27 rows).
func detectLang(s string) (lang string, ok bool) {
	c := countScripts(s)
	if c.hira+c.han == 0 {
		if c.kata > 0 {
			return langJa, true
		}
		return "", false
	}
	if float64(c.hira)/float64(c.hira+c.han) < hiraganaShare {
		return langZhHans, true
	}
	return langJa, true
}
