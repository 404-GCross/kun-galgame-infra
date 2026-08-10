package actrie

type node struct {
	children map[byte]int
	fail     int
	dict     int
	out      []int
}

type Matcher struct {
	nodes     []node
	nPatterns int
}

func Build(patterns [][]byte) *Matcher {
	m := &Matcher{nPatterns: len(patterns)}
	m.nodes = []node{newNode()}
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

func (m *Matcher) buildLinks() {
	queue := make([]int, 0, len(m.nodes))
	for _, child := range m.nodes[0].children {
		m.nodes[child].fail = 0
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		f := m.nodes[u].fail
		if len(m.nodes[f].out) > 0 {
			m.nodes[u].dict = f
		} else {
			m.nodes[u].dict = m.nodes[f].dict
		}
		for b, v := range m.nodes[u].children {
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

func (m *Matcher) Match(text []byte) []int {
	if m.nPatterns == 0 || len(m.nodes) <= 1 {
		return nil
	}
	hit := make([]bool, m.nPatterns)
	any := false
	state := 0
	for _, b := range text {
		for state != 0 {
			if _, ok := m.nodes[state].children[b]; ok {
				break
			}
			state = m.nodes[state].fail
		}
		if next, ok := m.nodes[state].children[b]; ok {
			state = next
		}
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
