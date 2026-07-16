package norm

import "testing"

// Invisible / special code points, built from rune values so no literal
// zero-width byte (nor a BOM, illegal anywhere but byte 0) sits in the source.
var (
	zwsp = string(rune(0x200B)) // ZERO WIDTH SPACE (Cf)
	zwnj = string(rune(0x200C)) // ZERO WIDTH NON-JOINER (Cf)
	zwj  = string(rune(0x200D)) // ZERO WIDTH JOINER (Cf)
	bom  = string(rune(0xFEFF)) // ZERO WIDTH NO-BREAK SPACE / BOM (Cf)
	nbsp = string(rune(0x00A0)) // NO-BREAK SPACE (NFKC -> ASCII space)
	ideo = string(rune(0x3000)) // IDEOGRAPHIC SPACE (NFKC -> ASCII space)
)

// TestNormalize is the table-driven fold contract: NFKC (fullwidth -> half),
// case-folding, zero-width/format-char stripping, and whitespace collapse.
func TestNormalize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"ascii-lower", "BAD", "bad"},
		{"mixed-case", "BaD WoRd", "bad word"},
		{"fullwidth-latin", "ＢＡＤ", "bad"},
		{"fullwidth-latin-matches-halfwidth-store", "ＢＡＤ", Normalize("bad")},
		{"fullwidth-digits", "１２３", "123"},
		{"zero-width-space-inside-cjk", "坏" + zwsp + "词", "坏词"},
		{"zwnj-inside", "ab" + zwnj + "cd", "abcd"},
		{"zwj-inside", "ab" + zwj + "cd", "abcd"},
		{"bom-inside", "ab" + bom + "cd", "abcd"},
		{"collapse-inner-whitespace", "bad    word", "bad word"},
		{"collapse-mixed-whitespace", "bad\t\n word", "bad word"},
		{"trim-ends", "   bad word   ", "bad word"},
		{"fullwidth-space-collapses", "bad" + ideo + "word", "bad word"},
		{"nbsp-collapses", "bad" + nbsp + "word", "bad word"},
		{"cjk-preserved", "傻逼", "傻逼"},
		{"whitespace-only", "  \t \n ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Normalize(c.in); got != c.want {
				t.Fatalf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNormalizeIdempotent pins Normalize(Normalize(x)) == Normalize(x) — the
// invariant that lets terms be stored pre-normalized and matched without
// re-normalizing the stored side.
func TestNormalizeIdempotent(t *testing.T) {
	for _, in := range []string{
		"", "BAD", "ＢＡＤ", "坏" + zwsp + "词", "  bad   WORD  ", "傻逼" + ideo + "ＸＹＺ", "a" + zwnj + "b c",
	} {
		once := Normalize(in)
		if twice := Normalize(once); twice != once {
			t.Fatalf("not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}
