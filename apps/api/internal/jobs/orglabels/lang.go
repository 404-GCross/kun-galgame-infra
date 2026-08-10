package orglabels

import (
	"regexp"
	"strings"
)

const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
	langEn     = "en"
)

func detectLang(s string) string {
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return langJa
		}
	}
	return langZhHans
}

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

func normalizeText(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

var (
	reSpoilerSpan = regexp.MustCompile(`(?is)\[spoiler\].*?\[/spoiler\]`)
	reSpoilerOpen = regexp.MustCompile(`(?is)\[spoiler\].*\z`)
	reURLOpen     = regexp.MustCompile(`(?i)\[url=[^\]]*\]`)
	reSimpleTag   = regexp.MustCompile(`(?i)\[/?(?:url|b|i|u|s|quote|raw|code)\]`)
	reBlankRuns   = regexp.MustCompile(`\n{3,}`)
)

func stripVNDBMarkup(s string) string {
	s = reSpoilerSpan.ReplaceAllString(s, "")
	s = reSpoilerOpen.ReplaceAllString(s, "")
	s = reURLOpen.ReplaceAllString(s, "")
	s = reSimpleTag.ReplaceAllString(s, "")
	s = reBlankRuns.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
