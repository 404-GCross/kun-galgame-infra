package norm

import (
	"strings"
	"unicode"

	xnorm "golang.org/x/text/unicode/norm"
)

func Normalize(s string) string {
	s = strings.ToLower(xnorm.NFKC.String(s))

	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	wrote := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
			continue
		case unicode.IsSpace(r):
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
