package norm

import "testing"

var (
	zwsp = string(rune(0x200B))
	zwnj = string(rune(0x200C))
	zwj  = string(rune(0x200D))
	bom  = string(rune(0xFEFF))
	nbsp = string(rune(0x00A0))
	ideo = string(rune(0x3000))
)

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
