package wikizh

import "strings"

type stringsBuilder struct{ b strings.Builder }

func (s *stringsBuilder) line(v string) {
	s.b.WriteString(v)
	s.b.WriteByte('\n')
}

func (s *stringsBuilder) String() string { return s.b.String() }

func truncate(v string, max int) string {
	r := []rune(v)
	if len(r) <= max {
		return v
	}
	return string(r[:max]) + "…（略）"
}
