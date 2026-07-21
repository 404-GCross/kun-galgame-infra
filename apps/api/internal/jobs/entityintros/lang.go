package entityintros

import (
	"regexp"
	"strings"
)

// Language codes — the SHORT BCP-47 vocabulary shared by every intro surface
// (galgameIntroPivot ja/en/zh-Hans/zh-Hant and the bodyless work-intro writes).
// Character/person intros MUST speak the same vocabulary or a consumer's
// per-language selection mis-fires — the 55/56 lesson.
const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
	langEn     = "en"
)

// normalizeText makes staging text storable verbatim except for line endings:
// CRLF→LF (the Bangumi dump carries \r\n artifacts). No other cleaning for the
// bangumi lanes; the vndb lane additionally runs stripVNDBMarkup.
func normalizeText(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// detectLang mirrors bgmsummaries.detectLang (step 57) — the helper is
// unexported there and exporting it just for this sibling job would widen a
// shipped package's surface, so the 10-line heuristic is mirrored with this
// note instead (spec-sanctioned): a summary containing any hiragana/katakana
// ([ぁ-んァ-ヶ], i.e. U+3041–U+3093 / U+30A1–U+30F6) is Japanese; anything
// else is Simplified Chinese. Known imprecision, accepted as in 57: some
// Traditional-Chinese summaries are labeled zh-Hans (script-mixed texts make a
// character-set zh-Hant split arbitrary without an OpenCC-grade table).
func detectLang(s string) string {
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return langJa
		}
	}
	return langZhHans
}

// VNDB description markup (BBCode-ish). Survey of src_vndb.chars (59,314
// non-empty descriptions): [url=] 23,470 · [spoiler] 4,720 (230 unclosed) ·
// [i] 569 · [b] 365 · [u] 121 · [s] 6 · [quote] 3 · [raw]/[code] 0.
var (
	// A closed spoiler span is removed ENTIRELY, content included.
	reSpoilerSpan = regexp.MustCompile(`(?is)\[spoiler\].*?\[/spoiler\]`)
	// An UNCLOSED [spoiler] drops everything to the end of the text — the safe
	// direction (a formatting typo must never leak a spoiler).
	reSpoilerOpen = regexp.MustCompile(`(?is)\[spoiler\].*\z`)
	// Light markup is unwrapped: tags dropped, inner text kept. [url=…]→ the
	// link text survives, the target does not (an attribution line like
	// "[From [url=…]Wikipedia[/url]]" becomes "[From Wikipedia]").
	reURLOpen   = regexp.MustCompile(`(?i)\[url=[^\]]*\]`)
	reSimpleTag = regexp.MustCompile(`(?i)\[/?(?:url|b|i|u|s|quote|raw|code)\]`)
	// Span removal can leave runs of blank lines; collapse ≥3 newlines to a
	// paragraph break. This (plus the outer TrimSpace) is the only whitespace
	// editing beyond CRLF→LF, and only on the vndb lane.
	reBlankRuns = regexp.MustCompile(`\n{3,}`)
)

// stripVNDBMarkup prepares one VNDB description: spoiler spans removed
// entirely, light markup unwrapped, removal residue tidied. Reports whether at
// least one spoiler span was removed. Input is already CRLF-normalized.
func stripVNDBMarkup(s string) (out string, spoilerStripped bool) {
	if t := reSpoilerSpan.ReplaceAllString(s, ""); t != s {
		s, spoilerStripped = t, true
	}
	if t := reSpoilerOpen.ReplaceAllString(s, ""); t != s {
		s, spoilerStripped = t, true
	}
	s = reURLOpen.ReplaceAllString(s, "")
	s = reSimpleTag.ReplaceAllString(s, "")
	s = reBlankRuns.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s), spoilerStripped
}
