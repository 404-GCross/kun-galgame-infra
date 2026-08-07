package derivedseries

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// minPrefixRunes is how much shared prefix counts as a name. Two runes of
// agreement is noise in Japanese — half the catalogue starts with 恋 or 少女 —
// while three is already a title fragment a reader recognises.
const minPrefixRunes = 3

// trailingJunk is what a truncated shared prefix ends in when it cut a title
// mid-numbering: the separators, brackets and digits that were about to
// introduce "2" or "～after story～". Trimmed so "Clannad " and "Clannad -
// " both land on "Clannad".
const trailingJunk = " \t　-–—~〜_:：/／\\|｜.。、,，·・+&＆'\"「」『』【】〈〉《》()（）[]［］{}<>0123456789"

// nameComponent picks a display name for a component mechanically: the longest
// common prefix of the members' NFKC-folded display titles when it survives
// trimming at 3+ runes, otherwise the earliest-released member's title verbatim.
//
// LLM naming is a later wave on purpose. A machine-picked prefix is either
// obviously right ("シンフォニック=レイン") or obviously a prefix, and both are
// honest; a generated name is neither, and this lane's whole claim is that it
// only publishes what the data already said. The fallback adds no suffix for
// the same reason — "Clannad" as the name of the Clannad group is a fact,
// "Clannad Series" would be an invention.
//
// Returns the name and whether the prefix rule produced it (the run reports the
// split so a human can see how much of the lane is really named).
func nameComponent(titles []string, earliestTitle string) (string, bool) {
	if p := commonPrefix(titles); utf8.RuneCountInString(p) >= minPrefixRunes {
		return p, true
	}
	return earliestTitle, false
}

// commonPrefix is the trimmed longest common prefix of the NFKC-folded titles.
// Empty when the titles share nothing usable.
func commonPrefix(titles []string) string {
	folded := make([]string, 0, len(titles))
	for _, t := range titles {
		t = strings.TrimSpace(norm.NFKC.String(t))
		if t == "" {
			continue // a title-less member cannot veto the others' agreement
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
