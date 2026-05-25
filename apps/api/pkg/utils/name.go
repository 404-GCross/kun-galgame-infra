package utils

import (
	"regexp"
	"strings"
)

// invisibleNameChars are Unicode codepoints that are visually empty
// (whitespace / zero-width / format control / unassigned space-like)
// and MUST NOT appear in a username. Allowing any of these would let
// an attacker register a name that renders identically to an existing
// account ("kun" vs "kun<ZWSP>") and impersonate them.
//
// Written as `\u` / `\U` escapes — NOT raw bytes — for two reasons:
//   1. Go's parser rejects U+FEFF (BOM) inside string literals; raw-
//      byte form fails to compile.
//   2. Raw zero-width bytes silently corrupt on any future Read/Edit
//      roundtrip through editors that normalize whitespace.
//
// Mirrored from the legacy JS isValidName rule. The JS source used 4-
// digit `\uXXXX` escapes for some plane-1 codepoints (e.g. JS `ᴕ9`
// parses as `ᴕ` + `9` — a latent bug that made those entries
// no-ops). Go's `\U00012345` 8-digit escape works correctly for those,
// so the INTENT of the legacy rule actually executes here. If you sync
// the JS side, fix it there too.
var invisibleNameChars = []string{
	"	", // CHARACTER TABULATION
	" ", // SPACE
	" ", // NO-BREAK SPACE
	"­", // SOFT HYPHEN
	"͏", // COMBINING GRAPHEME JOINER
	"؜", // ARABIC LETTER MARK
	"ᅟ", // HANGUL CHOSEONG FILLER
	"ᅠ", // HANGUL JUNGSEONG FILLER
	"឴", // KHMER VOWEL INHERENT AQ
	"឵", // KHMER VOWEL INHERENT AA
	"᠎", // MONGOLIAN VOWEL SEPARATOR
	" ", // EN QUAD
	" ", // EM QUAD
	" ", // EN SPACE
	" ", // EM SPACE
	" ", // THREE-PER-EM SPACE
	" ", // FOUR-PER-EM SPACE
	" ", // SIX-PER-EM SPACE
	" ", // FIGURE SPACE
	" ", // PUNCTUATION SPACE
	" ", // THIN SPACE
	" ", // HAIR SPACE
	"​", // ZERO WIDTH SPACE
	"‌", // ZERO WIDTH NON-JOINER
	"‍", // ZERO WIDTH JOINER
	"‎", // LEFT-TO-RIGHT MARK
	"‏", // RIGHT-TO-LEFT MARK
	" ", // NARROW NO-BREAK SPACE
	" ", // MEDIUM MATHEMATICAL SPACE
	"⁠", // WORD JOINER
	"⁡", // FUNCTION APPLICATION
	"⁢", // INVISIBLE TIMES
	"⁣", // INVISIBLE SEPARATOR
	"⁤", // INVISIBLE PLUS
	"⁥", // (reserved)
	"⁪", // INHIBIT SYMMETRIC SWAPPING
	"⁫", // ACTIVATE SYMMETRIC SWAPPING
	"⁬", // INHIBIT ARABIC FORM SHAPING
	"⁭", // ACTIVATE ARABIC FORM SHAPING
	"⁮", // NATIONAL DIGIT SHAPES
	"⁯", // NOMINAL DIGIT SHAPES
	"　", // IDEOGRAPHIC SPACE
	"⠀", // BRAILLE PATTERN BLANK
	"ㅤ", // HANGUL FILLER
	"\uFEFF", // ZERO WIDTH NO-BREAK SPACE (BOM)
	"ﾠ", // HALFWIDTH HANGUL FILLER
	"\U0001D159", // MUSICAL SYMBOL NULL NOTEHEAD
	"\U0001D173", // MUSICAL SYMBOL BEGIN BEAM
	"\U0001D174", // MUSICAL SYMBOL END BEAM
	"\U0001D175", // MUSICAL SYMBOL BEGIN TIE
	"\U0001D176", // MUSICAL SYMBOL END TIE
	"\U0001D177", // MUSICAL SYMBOL BEGIN SLUR
	"\U0001D178", // MUSICAL SYMBOL END SLUR
	"\U0001D179", // MUSICAL SYMBOL BEGIN PHRASE
	"\U0001D17A", // MUSICAL SYMBOL END PHRASE
	"\U000E0020", // TAG SPACE
}

// validNameRegex mirrors the legacy JS rule
// `/^[\p{L}\p{N}!~_@#$%^&*()+=-]{1,17}$/u`:
// any Unicode letter or number, plus the listed punctuation, 1 to 17
// chars total. Go's `\pL` / `\pN` are equivalent to JS `\p{L}` / `\p{N}`.
var validNameRegex = regexp.MustCompile(`^[\pL\pN!~_@#$%^&*()+=\-]{1,17}$`)

// IsValidName enforces the legacy username rule:
//   - 1 to 17 characters
//   - Unicode letter (\pL), Unicode number (\pN), or one of !~_@#$%^&*()+=-
//   - rejects 50+ visually-empty codepoints (see invisibleNameChars
//     for the rationale)
//
// Applied at registration, username change, and login. Login pre-check
// is purely a fast-fail — if a legacy account exists with a name that
// no longer passes, that account can still log in via its email instead.
func IsValidName(name string) bool {
	for _, ch := range invisibleNameChars {
		if strings.Contains(name, ch) {
			return false
		}
	}
	return validNameRegex.MatchString(name)
}
