package bgmsummaries

import "strings"

// Language codes — the SHORT BCP-47 vocabulary of the claimed-side read-face
// pivot (galgameIntroPivot: intro_ja_jp→"ja", intro_en_us→"en",
// intro_zh_cn→"zh-Hans", intro_zh_tw→"zh-Hant") and of the step-52/55 bodyless
// writes ("en"/"ja"). Bodyless and claimed MUST share one lang vocabulary or
// the consumer's per-language selection mis-fires — the 55/56 lesson.
const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
)

// normalizeSummary makes the dump text storable verbatim except for line
// endings: CRLF→LF (the Bangumi dump carries \r\n artifacts — 59,819 of the
// type=4 summaries; verified that every \r is part of a \r\n pair, so no bare
// \r handling is needed). No other cleaning, same as steps 52/55.
func normalizeSummary(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// detectLang implements the spec-pinned heuristic: a summary containing any
// hiragana/katakana ([ぁ-んァ-ヶ], i.e. U+3041–U+3093 / U+30A1–U+30F6) is
// Japanese; anything else is Simplified Chinese.
//
// Known imprecision, accepted for v1 (spec §钉死的策略): some
// Traditional-Chinese summaries are labeled zh-Hans. A zh-Hant split was
// deliberately NOT attempted — sampled non-kana summaries are script-MIXED
// within a single text (e.g. "隸屬…他们接获命令"), so a character-set
// heuristic would label arbitrarily; a reliable split needs an OpenCC-grade
// table, unjustified for ~236 rows. Report notes the trade-off.
func detectLang(s string) string {
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return langJa
		}
	}
	return langZhHans
}

// isBodyless reports whether a catalog_work is bodyless (site NULL or ''). The
// XOR guard: native intro rows are written ONLY for bodyless works — a claimed
// work's intro is bridged from its product DB, never copied (§2/§8.D).
func isBodyless(site *string) bool { return site == nil || *site == "" }
