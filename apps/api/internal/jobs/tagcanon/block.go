package tagcanon

import (
	"sort"
)

type candName struct {
	SourceID  int16
	SourceKey string
	Name      string
	Norm      string
	Orig      string
	Usage     int
	workIDs   map[int64]struct{}
}

type candPair struct {
	A     candName
	B     candName
	Block string
}

type blockOpts struct {
	MaxEdit    int
	MinLen     int
	CooccurJac float64
	MaxPairs   int
	MinCooccur int
}

func (o blockOpts) withDefaults() blockOpts {
	if o.MaxEdit == 0 {
		o.MaxEdit = 1
	}
	if o.MinLen == 0 {
		o.MinLen = 2
	}
	if o.CooccurJac == 0 {
		o.CooccurJac = 0.30
	}
	if o.MaxPairs == 0 {
		o.MaxPairs = 50000
	}
	if o.MinCooccur == 0 {
		o.MinCooccur = 3
	}
	return o
}

type blockStats struct {
	PoolBySource map[string]int
	Comparisons  int
	Substring    int
	Edit         int
	Cooccur      int
	Total        int
	Capped       bool
}

func buildCandidatePairs(pool []candName, opts blockOpts) ([]candPair, blockStats) {
	opts = opts.withDefaults()
	st := blockStats{PoolBySource: map[string]int{}}
	for _, c := range pool {
		st.PoolBySource[c.SourceKey]++
	}

	seen := map[[2]string]struct{}{}
	var pairs []candPair
	emit := func(a, b candName, rule string) bool {
		ka := a.SourceKey + ":" + a.Name
		kb := b.SourceKey + ":" + b.Name
		key := [2]string{ka, kb}
		if ka > kb {
			key = [2]string{kb, ka}
		}
		if _, dup := seen[key]; dup {
			return true
		}
		seen[key] = struct{}{}
		if a.SourceID > b.SourceID || (a.SourceID == b.SourceID && a.Name > b.Name) {
			a, b = b, a
		}
		pairs = append(pairs, candPair{A: a, B: b, Block: rule})
		switch rule {
		case "substring":
			st.Substring++
		case "edit":
			st.Edit++
		case "cooccur":
			st.Cooccur++
		}
		return len(pairs) < opts.MaxPairs
	}

	for i := range len(pool) {
		a := pool[i]
		ra := []rune(a.Norm)
		if len(ra) < opts.MinLen {
			continue
		}
		for j := i + 1; j < len(pool); j++ {
			b := pool[j]
			if a.SourceID == b.SourceID {
				continue
			}
			rb := []rune(b.Norm)
			if len(rb) < opts.MinLen {
				continue
			}
			st.Comparisons++

			if containsWord(a.Norm, b.Norm) || containsWord(b.Norm, a.Norm) {
				if !emit(a, b, "substring") {
					st.Capped = true
					goto done
				}
				continue
			}
			if abs(len(ra)-len(rb)) <= opts.MaxEdit && levenshtein(a.Norm, b.Norm) <= opts.MaxEdit {
				if !emit(a, b, "edit") {
					st.Capped = true
					goto done
				}
				continue
			}
			if a.workIDs != nil && b.workIDs != nil {
				if inter := intersectCount(a.workIDs, b.workIDs); inter >= opts.MinCooccur {
					union := len(a.workIDs) + len(b.workIDs) - inter
					if union > 0 && float64(inter)/float64(union) >= opts.CooccurJac {
						if !emit(a, b, "cooccur") {
							st.Capped = true
							goto done
						}
					}
				}
			}
		}
	}
done:
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A.SourceKey != pairs[j].A.SourceKey {
			return pairs[i].A.SourceKey < pairs[j].A.SourceKey
		}
		if pairs[i].A.Name != pairs[j].A.Name {
			return pairs[i].A.Name < pairs[j].A.Name
		}
		if pairs[i].B.SourceKey != pairs[j].B.SourceKey {
			return pairs[i].B.SourceKey < pairs[j].B.SourceKey
		}
		return pairs[i].B.Name < pairs[j].B.Name
	})
	st.Total = len(pairs)
	return pairs, st
}

func containsWord(haystack, needle string) bool {
	if haystack == "" || needle == "" || haystack == needle {
		return false
	}
	if len([]rune(needle)) < 2 {
		return false
	}
	return len(needle) < len(haystack) && stringsContains(haystack, needle)
}

func stringsContains(s, sub string) bool {
	return indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 || m > n {
		return -1
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func intersectCount(a, b map[int64]struct{}) int {
	if len(b) < len(a) {
		a, b = b, a
	}
	n := 0
	for k := range a {
		if _, ok := b[k]; ok {
			n++
		}
	}
	return n
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
