// Package norm is the Tier0 word-list normalization choke point (step 05; doc
// 18 §6). Every term entering the registry and every text checked against it
// passes through Normalize — one function, one place — so the fold set stays
// consistent across the admin CRUD face, the sync /trust/check face, and the
// async scan worker.
//
// v0 fold set:
//
//  1. NFKC — Unicode compatibility composition (fullwidth ＢＡＤ → BAD, fullwidth
//     digits/spaces → ASCII, ligatures decomposed).
//  2. lower-case.
//  3. strip Unicode Cf (format) characters — zero-width space / ZWNJ / ZWJ /
//     BOM and friends — so a term hidden with a zero-width char inside still
//     matches (坏​词 → 坏词).
//  4. collapse runs of whitespace to a single ASCII space and trim the ends.
//
// EXPLICITLY OUT of v0: homoglyph folding and pinyin folding (doc 18's full
// vision). They are deferred triggered work, and the single-choke-point design
// is exactly so they can later be added HERE without touching any caller.
package norm

import (
	"strings"
	"unicode"

	xnorm "golang.org/x/text/unicode/norm"
)

// Normalize returns the canonical form of s for Tier0 matching. It is
// idempotent: Normalize(Normalize(s)) == Normalize(s).
func Normalize(s string) string {
	// 1. NFKC compatibility composition, then 2. lower-case.
	s = strings.ToLower(xnorm.NFKC.String(s))

	// 3. strip Cf format chars, 4. collapse whitespace — a single pass.
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	wrote := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
			// Zero-width / format char: drop it entirely (do not treat as a
			// break), so 坏​词 folds to 坏词.
			continue
		case unicode.IsSpace(r):
			// Defer the space so a trailing run collapses to nothing and an
			// inner run collapses to exactly one ASCII space.
			pendingSpace = wrote
		default:
			if pendingSpace {
				b.WriteByte(' ')
				pendingSpace = false
			}
			b.WriteRune(r)
			wrote = true
		}
	}
	return b.String()
}
