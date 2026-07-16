// Package actrie is a small, dependency-free byte-level Aho-Corasick multi-
// pattern matcher. It backs the Tier0 term matcher (trust/service): the linear
// per-term strings.Contains scan does not survive tens of thousands of terms, so
// the active-term set is compiled once per snapshot reload into an automaton and
// every /trust/check runs the text through it in a single pass.
//
// The automaton is byte-level (UTF-8 patterns are fed as raw bytes), so a hit is
// exactly a normalized byte-substring containment — semantically identical to the
// strings.Contains it replaces. Payloads are opaque int indexes supplied at build
// time; Match reports the DISTINCT set of payloads whose pattern occurs anywhere
// in the input, in ascending order. A Matcher is immutable after Build and safe
// for concurrent use.
package actrie

// node is one trie state. children are the goto edges (map-based, as the term
// alphabet is the full byte range but sparse per node). out lists the payload
// indexes of patterns terminating exactly at this node; dict is the dictionary-
// suffix link — the nearest strict fail-ancestor that is itself terminal (-1 if
// none), which lets Match enumerate all suffix matches without walking every
// fail link.
type node struct {
	children map[byte]int
	fail     int
	dict     int
	out      []int
}

// Matcher is a compiled Aho-Corasick automaton. nPatterns is the number of
// patterns handed to Build (including any empty ones, which are never inserted
// and so never match) — it bounds the payload space Match reports over.
type Matcher struct {
	nodes     []node
	nPatterns int
}

// Build compiles the patterns into an automaton. Pattern k carries payload k.
// An empty pattern is skipped (never inserted, never emitted), mirroring the
// matcher's defensive empty-term skip — strings.Contains(x, "") would be true,
// which is never the intent for a term list. Duplicate patterns are allowed:
// their payloads all attach to the same terminal node and are all emitted.
func Build(patterns [][]byte) *Matcher {
	m := &Matcher{nPatterns: len(patterns)}
	m.nodes = []node{newNode()} // node 0 is the root
	for k, p := range patterns {
		if len(p) == 0 {
			continue
		}
		cur := 0
		for _, b := range p {
			next, ok := m.nodes[cur].children[b]
			if !ok {
				next = len(m.nodes)
				m.nodes = append(m.nodes, newNode())
				m.nodes[cur].children[b] = next
			}
			cur = next
		}
		m.nodes[cur].out = append(m.nodes[cur].out, k)
	}
	m.buildLinks()
	return m
}

func newNode() node { return node{children: map[byte]int{}, dict: -1} }

// buildLinks computes fail and dictionary-suffix links via BFS over the trie.
func (m *Matcher) buildLinks() {
	queue := make([]int, 0, len(m.nodes))
	// Depth-1 nodes fail to the root.
	for _, child := range m.nodes[0].children {
		m.nodes[child].fail = 0
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		// The dictionary link points at the nearest terminal reachable via fail
		// links; collapse the chain so Match walks only terminal nodes.
		f := m.nodes[u].fail
		if len(m.nodes[f].out) > 0 {
			m.nodes[u].dict = f
		} else {
			m.nodes[u].dict = m.nodes[f].dict
		}
		for b, v := range m.nodes[u].children {
			// fail[v] = the deepest proper suffix state that has an edge on b.
			s := m.nodes[u].fail
			for s != 0 {
				if _, ok := m.nodes[s].children[b]; ok {
					break
				}
				s = m.nodes[s].fail
			}
			if t, ok := m.nodes[s].children[b]; ok && t != v {
				m.nodes[v].fail = t
			} else {
				m.nodes[v].fail = 0
			}
			queue = append(queue, v)
		}
	}
}

// Match runs text through the automaton and returns the distinct payload indexes
// whose pattern occurs as a byte-substring of text, in ascending order (nil when
// there are no hits). Ascending order mirrors the payload/insertion order, so the
// caller sees hits in the same order the old linear scan produced them.
func (m *Matcher) Match(text []byte) []int {
	if m.nPatterns == 0 || len(m.nodes) <= 1 {
		return nil
	}
	hit := make([]bool, m.nPatterns)
	any := false
	state := 0
	for _, b := range text {
		// Follow fail links until b has an edge (or we fall back to the root).
		for state != 0 {
			if _, ok := m.nodes[state].children[b]; ok {
				break
			}
			state = m.nodes[state].fail
		}
		if next, ok := m.nodes[state].children[b]; ok {
			state = next
		}
		// Emit this state's outputs and every dictionary-suffix output.
		for u := state; u != -1; u = m.nodes[u].dict {
			for _, k := range m.nodes[u].out {
				hit[k] = true
				any = true
			}
		}
	}
	if !any {
		return nil
	}
	res := make([]int, 0, 8)
	for i, h := range hit {
		if h {
			res = append(res, i)
		}
	}
	return res
}
