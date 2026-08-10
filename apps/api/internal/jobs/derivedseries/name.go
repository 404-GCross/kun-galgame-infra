package derivedseries

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const minPrefixRunes = 3

const trailingJunk = " \t　-–—~〜_:：/／\\|｜.。、,，·・+&＆'\"「」『』【】〈〉《》()（）[]［］{}<>0123456789"

func nameComponent(titles []string, earliestTitle string) (string, bool) {
	if p := commonPrefix(titles); utf8.RuneCountInString(p) >= minPrefixRunes {
		return p, true
	}
	return earliestTitle, false
}

func commonPrefix(titles []string) string {
	folded := make([]string, 0, len(titles))
	for _, t := range titles {
		t = strings.TrimSpace(norm.NFKC.String(t))
		if t == "" {
			continue
		}
		folded = append(folded, t)
	}
	if len(folded) < 2 {
		return ""
	}
	prefix := []rune(folded[0])
	for _, t := range folded[1:] {
		other := []rune(t)
		n := min(len(prefix), len(other))
		i := 0
		for i < n && prefix[i] == other[i] {
			i++
		}
		prefix = prefix[:i]
		if len(prefix) == 0 {
			return ""
		}
	}
	out := strings.TrimRightFunc(string(prefix), func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(trailingJunk, r)
	})
	return strings.TrimSpace(out)
}
