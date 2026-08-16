package textnorm

import (
	"fmt"
	"slices"
	"strings"
)

// stripRunes are removed everywhere in a value: zero-width formatters, all bidi
// controls, the word joiner and the BOM/ZWNBSP. U+FE0F (emoji variation
// selector) is deliberately NOT here — it is part of legitimate emoji like ⚠️.
var stripRunes = []rune{
	0x200B, 0x200C, 0x200D, 0x200E, 0x200F,
	0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
	0x2060, 0x2066, 0x2067, 0x2068, 0x2069,
	0xFEFF,
}

var trimRunes = []rune{' ', '\t', '\n', '\r', '　'}

const trimSetSQL = `E' \t\n\r' || chr(12288)`

var stripSet = func() map[rune]bool {
	m := make(map[rune]bool, len(stripRunes))
	for _, r := range stripRunes {
		m[r] = true
	}
	return m
}()

func Clean(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !stripSet[r] {
			b.WriteRune(r)
		}
	}
	return strings.TrimFunc(b.String(), func(r rune) bool {
		return slices.Contains(trimRunes, r)
	})
}

// CleanSQL is Clean's twin for a SQL expression. Strip first, trim second, the
// same way Clean does: writing btrim inside regexp_replace instead of around it
// makes the two disagree on a value whose ends are a zero-width character
// wrapping a space, where stripping first exposes a space the trim then removes.
func CleanSQL(expr string) string {
	return fmt.Sprintf("btrim(regexp_replace(%s, %s, '', 'g'), %s)", expr, stripClassSQL(), trimSetSQL)
}

func DirtyWhereSQL(col string) string {
	return fmt.Sprintf(
		"%[1]s IS NOT NULL AND (%[1]s ~ %[2]s OR %[1]s <> btrim(%[1]s, "+trimSetSQL+"))",
		col, stripClassSQL())
}

func stripClassSQL() string {
	chrs := make([]string, len(stripRunes))
	for i, r := range stripRunes {
		chrs[i] = fmt.Sprintf("chr(%d)", r)
	}
	return "('[' || " + strings.Join(chrs, "||") + " || ']')"
}
