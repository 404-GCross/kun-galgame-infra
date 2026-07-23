package tagcanon

import (
	"sort"
)

// candName is one pairing candidate: a non-junk, non-meta, not-yet-canonical
// source name enriched with its ORIGINAL form (doc 87 ruling 2) and — for the
// bangumi/dlsite lanes that share the catalog_work id space — its work-id set
// for co-occurrence blocking. vndb tags attach to galgame (wiki) rows in a
// DIFFERENT id space, so co-occurrence is only computed within/among
// bangumi+dlsite; vndb pairs rely on the literal/edit-distance signals.
type candName struct {
	SourceID  int16
	SourceKey string
	Name      string // zh display name = the source_map key
	Norm      string // NFKC+casefold+trim of Name (reused normalize)
	Orig      string // original form (EN/ja/verbatim)
	Usage     int
	workIDs   map[int64]struct{} // bangumi/dlsite only; nil for vndb
}

// candPair is one blocking-generated cross-source candidate pair (A.SourceID <
// B.SourceID, deterministic order) with the rule that surfaced it.
type candPair struct {
	A     candName
	B     candName
	Block string // "substring" | "edit" | "cooccur"
}

// blockOpts tunes the blocking pre-filter (doc 87 ruling 3: converge — never a
// 4,700² all-pairs sweep). Zero values fall back to the pinned defaults.
type blockOpts struct {
	MaxEdit    int     // max Levenshtein on norms (default 1)
	MinLen     int     // ignore names shorter than this in runes (default 2)
	CooccurJac float64 // min Jaccard of work-id sets to pair (default 0.30)
	MaxPairs   int     // hard budget; 0 = pinned 50,000
	MinCooccur int     // min shared works before a co-occurrence pair counts (default 3)
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

// blockStats reports how the pre-filter converged (for the report).
type blockStats struct {
	PoolBySource map[string]int
	Comparisons  int // cross-source name comparisons considered
	Substring    int
	Edit         int
	Cooccur      int
	Total        int // distinct candidate pairs emitted (== len returned)
	Capped       bool
}

// buildCandidatePairs converges the cross-source candidate set (doc 87 ruling
// 3). A pair (different sources) is emitted when ANY signal fires:
//   - substring: one norm literally contains the other (both ≥ MinLen);
//   - edit: Levenshtein(normA, normB) ≤ MaxEdit (length-guarded);
//   - cooccur: Jaccard of work-id sets ≥ CooccurJac (bangumi↔dlsite only).
//
// Each pair is emitted at most once; the first rule that fires labels it. The
// result is sorted deterministically. A blown budget is truncated and flagged.
func buildCandidatePairs(pool []candName, opts blockOpts) ([]candPair, blockStats) {
	opts = opts.withDefaults()
	st := blockStats{PoolBySource: map[string]int{}}
	for _, c := range pool {
		st.PoolBySource[c.SourceKey]++
	}

	seen := map[[2]string]struct{}{} // dedupe key: sorted "src:name" pair
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
		// Deterministic A/B order: lower source id first, then name.
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

	for i := 0; i < len(pool); i++ {
		a := pool[i]
		ra := []rune(a.Norm)
		if len(ra) < opts.MinLen {
			continue
		}
		for j := i + 1; j < len(pool); j++ {
			b := pool[j]
			if a.SourceID == b.SourceID {
				continue // cross-source only
			}
			rb := []rune(b.Norm)
			if len(rb) < opts.MinLen {
				continue
			}
			st.Comparisons++

			// 1) substring containment (cheap, high-precision).
			if containsWord(a.Norm, b.Norm) || containsWord(b.Norm, a.Norm) {
				if !emit(a, b, "substring") {
					st.Capped = true
					goto done
				}
				continue
			}
			// 2) edit distance — length-guarded so we never run it on wildly
			//    different lengths (|Δlen| > MaxEdit can never be within MaxEdit).
			if abs(len(ra)-len(rb)) <= opts.MaxEdit && levenshtein(a.Norm, b.Norm) <= opts.MaxEdit {
				if !emit(a, b, "edit") {
					st.Capped = true
					goto done
				}
				continue
			}
			// 3) co-occurrence — only when both carry work-id sets (bgm/dlsite).
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

// containsWord reports whether haystack contains needle as a substring, with a
// guard that needle is at least 2 runes (a 1-rune "substring" is noise). Both
// inputs are already normalized.
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
	// tiny wrapper so the intent (contains, not equals) reads clearly at the
	// call sites; avoids importing strings just for one use here.
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

// levenshtein is the classic edit distance on runes (short tag names — the O(nm)
// table is fine). Used only after a length guard, so n,m are close.
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
	// iterate the smaller set
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
