package entityintros

import (
	"regexp"
	"strings"
)

const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
	langEn     = "en"
)

func normalizeText(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

func detectLang(s string) string {
	for _, r := range s {
		if (r >= 'ぁ' && r <= 'ん') || (r >= 'ァ' && r <= 'ヶ') {
			return langJa
		}
	}
	return langZhHans
}

var (
	reSpoilerSpan = regexp.MustCompile(`(?is)\[spoiler\].*?\[/spoiler\]`)
	reSpoilerOpen = regexp.MustCompile(`(?is)\[spoiler\].*\z`)
	reURLOpen     = regexp.MustCompile(`(?i)\[url=[^\]]*\]`)
	reSimpleTag   = regexp.MustCompile(`(?i)\[/?(?:url|b|i|u|s|quote|raw|code)\]`)
	reBlankRuns   = regexp.MustCompile(`\n{3,}`)
)

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
