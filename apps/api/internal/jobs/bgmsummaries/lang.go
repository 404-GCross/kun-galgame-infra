package bgmsummaries

import "strings"

const (
	langJa     = "ja"
	langZhHans = "zh-Hans"
)

const hiraganaShare = 0.05

func normalizeSummary(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

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
