package orglabels

import (
	"regexp"
	"strings"
)

// Language classification + VNDB markup stripping, copied from the C1
// entityintros precedent (unexported there) with a 3-way VNDB variant per
// refs/proj/83 裁定6. Deliberate duplication: the source functions are
// unexported and this is a self-contained handful of lines.

const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
	langEn     = "en"
)

// detectLang is the step-57 two-way heuristic: any hiragana/katakana ⇒ ja,
// otherwise zh-Hans. Used for Bangumi summaries.
func detectLang(s string) string {
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return langJa
		}
	}
	return langZhHans
}

// detectLangVNDB is the 3-way variant for VNDB producer descriptions: kana ⇒
// ja; else any Han ideograph ⇒ zh-Hans; else (pure latin) ⇒ en. VNDB writes
// most descriptions in English, but a minority are Japanese/Chinese.
func detectLangVNDB(s string) string {
	hasHan := false
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return langJa
		}
		if r >= 0x4E00 && r <= 0x9FFF {
			hasHan = true
		}
	}
	if hasHan {
		return langZhHans
	}
	return langEn
}

// normalizeText is the verbatim-except-CRLF normalizer.
func normalizeText(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// VNDB description markup (BBCode-ish) — same handling as entityintros.
var (
	reSpoilerSpan = regexp.MustCompile(`(?is)\[spoiler\].*?\[/spoiler\]`)
	reSpoilerOpen = regexp.MustCompile(`(?is)\[spoiler\].*\z`)
	reURLOpen     = regexp.MustCompile(`(?i)\[url=[^\]]*\]`)
	reSimpleTag   = regexp.MustCompile(`(?i)\[/?(?:url|b|i|u|s|quote|raw|code)\]`)
	reBlankRuns   = regexp.MustCompile(`\n{3,}`)
)

// stripVNDBMarkup removes spoiler spans entirely, unwraps light markup, and
// tidies the removal residue. Input is already CRLF-normalized.
func stripVNDBMarkup(s string) string {
	s = reSpoilerSpan.ReplaceAllString(s, "")
	s = reSpoilerOpen.ReplaceAllString(s, "")
	s = reURLOpen.ReplaceAllString(s, "")
	s = reSimpleTag.ReplaceAllString(s, "")
	s = reBlankRuns.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
