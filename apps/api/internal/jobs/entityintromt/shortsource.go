package entityintromt

import "unicode"

// minSourceRunes is the substance floor for a translatable source: fewer
// letters/ideographs than this and there is nothing to translate — skip
// before spending an LLM call. (This is NOT the wave-173 empty-output
// refusal class: those sources measured >50 chars of real text; their
// empty answers have a different, still-undiagnosed cause.)
const minSourceRunes = 10

// substanceRunes counts the runes that carry translatable content — letters,
// ideographs and digits. Punctuation, whitespace and markup glyphs don't
// count: a source made only of those is as untranslatable as an empty one.
func substanceRunes(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}
