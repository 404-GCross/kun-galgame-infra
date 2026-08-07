package derivedseries

import (
	"sort"
	"strings"

	"api/internal/jobs/seriesorder"

	"golang.org/x/text/unicode/norm"
)

// crossoverGuardRunes is how much shared prefix between an FD's TARGETS proves
// they are the same franchise (duplicate rows, a renamed sequel, a translated
// row) rather than separate lines. Two runes is enough here — unlike naming,
// the comparison is between the handful of works ONE fandisc points at, not
// across the whole catalogue, so two runes of agreement is real evidence
// ("亜美・風立ちぬ" vs "亜美 ～風立ちぬ～" agree on exactly two).
const crossoverGuardRunes = 2

// crossoverBridges finds the works whose weak edges chain SEPARATE lines into
// one component: the crossover fandisc problem (wave 185; series 749's Terios
// blob was five unrelated lines held together by テリぷら-style cross-title FDs).
//
// A work is a bridge only when all three hold:
//
//   - it carries no strong edge itself (it is a satellite, not a line entry);
//   - it is the SATELLITE side (edge side a — "a fandisc_of b") of weak edges
//     whose targets sit in two or more different strong-edge clusters. Direction
//     matters: a base game with three of its own fandiscs pointing AT it is the
//     hub of one family, not a bridge between three;
//   - its targets do not all share a name: when every pair of target titles
//     agrees on crossoverGuardRunes+ runes (NFKC, case-folded), the "separate
//     lines" are one franchise split over duplicate or renamed rows, and cutting
//     would shatter a real family.
//
// Bridges get their outgoing weak edges dropped before clustering; the bridge
// itself usually ends up a singleton and is worklisted rather than published —
// a work that belongs to several lines at once has no single honest series.
func crossoverBridges(works []int64, edgesByWork map[int64][]seriesorder.Edge,
	titles map[int64]string) []int64 {
	hasStrong := map[int64]bool{}
	outWeak := map[int64][]int64{}
	sd := map[int64]int64{}
	for _, w := range works {
		sd[w] = w
	}
	var find func(int64) int64
	find = func(x int64) int64 {
		if sd[x] != x {
			sd[x] = find(sd[x])
		}
		return sd[x]
	}
	for _, w := range works {
		for _, e := range edgesByWork[w] {
			if e.A != w { // walk each edge once, from its a side
				continue
			}
			if StrongEdgeTypes[e.Type] {
				hasStrong[e.A], hasStrong[e.B] = true, true
				ra, rb := find(e.A), find(e.B)
				if ra != rb {
					if rb < ra {
						ra, rb = rb, ra
					}
					sd[rb] = ra
				}
			} else {
				outWeak[e.A] = append(outWeak[e.A], e.B)
			}
		}
	}

	var bridges []int64
	for _, w := range works {
		if hasStrong[w] || len(outWeak[w]) < 2 {
			continue
		}
		roots := map[int64]struct{}{}
		for _, tgt := range outWeak[w] {
			roots[find(tgt)] = struct{}{}
		}
		if len(roots) < 2 {
			continue
		}
		if sameFranchise(outWeak[w], titles) {
			continue
		}
		bridges = append(bridges, w)
	}
	sort.Slice(bridges, func(i, j int) bool { return bridges[i] < bridges[j] })
	return bridges
}

// sameFranchise reports whether every pair of target titles shares the guard
// prefix — the "these lines are really one franchise" escape hatch.
func sameFranchise(targets []int64, titles map[int64]string) bool {
	folded := make([]string, 0, len(targets))
	for _, t := range targets {
		if s := strings.ToLower(norm.NFKC.String(titles[t])); strings.TrimSpace(s) != "" {
			folded = append(folded, s)
		}
	}
	for i := 0; i < len(folded); i++ {
		for j := i + 1; j < len(folded); j++ {
			if commonPrefixRunes(folded[i], folded[j]) < crossoverGuardRunes {
				return false
			}
		}
	}
	return true
}

func commonPrefixRunes(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && ra[n] == rb[n] {
		n++
	}
	return n
}

// pruneBridgeEdges rebuilds the adjacency without the bridges' outgoing weak
// edges. Everything else — including weak edges pointing AT a bridge — stays.
func pruneBridgeEdges(edgesByWork map[int64][]seriesorder.Edge, bridges []int64) (map[int64][]seriesorder.Edge, int) {
	cut := map[int64]bool{}
	for _, b := range bridges {
		cut[b] = true
	}
	out := make(map[int64][]seriesorder.Edge, len(edgesByWork))
	dropped := map[[2]int64]bool{}
	for w, es := range edgesByWork {
		keep := es[:0:0]
		for _, e := range es {
			if !StrongEdgeTypes[e.Type] && cut[e.A] {
				dropped[[2]int64{e.A, e.B}] = true
				continue
			}
			keep = append(keep, e)
		}
		out[w] = keep
	}
	return out, len(dropped)
}
