package wikizh

import "strings"

// stringsBuilder is a line-oriented builder — the packets are line-structured
// and a plain Builder makes that hard to read at the call site.
type stringsBuilder struct{ b strings.Builder }

func (s *stringsBuilder) line(v string) {
	s.b.WriteString(v)
	s.b.WriteByte('\n')
}

func (s *stringsBuilder) String() string { return s.b.String() }

// truncate caps one field of a packet. A handful of wiki intros run to 3k+
// characters, and three of those in one chunked request is enough to push a
// reasoning model past its output budget mid-reply — which loses the whole
// chunk, not one item.
func truncate(v string, max int) string {
	r := []rune(v)
	if len(r) <= max {
		return v
	}
	return string(r[:max]) + "…（略）"
}
