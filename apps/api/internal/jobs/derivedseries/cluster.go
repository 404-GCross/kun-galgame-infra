package derivedseries

import (
	"sort"

	"api/internal/jobs/seriesorder"
)

// GiantSize is the member count at which a component stops being a series and
// starts being a franchise blob. The relation graph has no notion of "how far
// apart" two works are, so one weak edge between two unrelated lines merges
// them forever: the 30+ bucket is 20 components holding 995 works, which is a
// twentieth of the components carrying a twentieth of the coverage and every
// one of them a name no mechanical rule could pick.
const GiantSize = 30

// StrongEdgeTypes is the subset a giant component is re-clustered on:
// sequel_of and same_series only. side_story_of and fandisc_of are the edges
// that ATTACH satellites to a line, so they are exactly the ones that chain
// separate lines together through a shared spin-off — dropping them splits the
// blob back into the lines it was made of, at the cost of leaving each line's
// own fandiscs behind (which the overlap-free rule then treats as their own
// tiny components or as nothing at all).
var StrongEdgeTypes = map[int64]bool{
	seriesorder.RelSequelOf:   true,
	seriesorder.RelSameSeries: true,
}

// component is one connected group of works, sorted ascending.
type component []int64

// components computes the connected components of `works` under `edges`,
// keeping only groups of 2 or more. A single work with no edge is not a series.
//
// `keep` filters which edges participate (nil = all of them) — the giant split
// reuses this function with the strong-edge predicate.
func components(works []int64, edgesByWork map[int64][]seriesorder.Edge,
	keep func(seriesorder.Edge) bool) []component {
	member := make(map[int64]struct{}, len(works))
	for _, w := range works {
		member[w] = struct{}{}
	}
	parent := make(map[int64]int64, len(works))
	for _, w := range works {
		parent[w] = w
	}
	var find func(int64) int64
	find = func(x int64) int64 {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int64) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// Lower root always wins, so the same graph always yields the same
		// representative — the external id is derived from the members, but a
		// stable walk keeps the output diffable run to run.
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}
	for _, w := range works {
		for _, e := range edgesByWork[w] {
			if keep != nil && !keep(e) {
				continue
			}
			if _, ok := member[e.A]; !ok {
				continue
			}
			if _, ok := member[e.B]; !ok {
				continue
			}
			union(e.A, e.B)
		}
	}
	groups := map[int64][]int64{}
	for _, w := range works {
		r := find(w)
		groups[r] = append(groups[r], w)
	}
	out := make([]component, 0, len(groups))
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(i, j int) bool { return g[i] < g[j] })
		out = append(out, g)
	}
	// Deterministic order by the component's own identity (its smallest work).
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// splitGiant re-clusters an oversized component on the strong edges alone.
// Sub-groups that fall below 2 members are dropped rather than kept as
// singletons: they are what is left of a line after its connective tissue was
// removed, not a series anyone would want to read.
func splitGiant(c component, edgesByWork map[int64][]seriesorder.Edge) []component {
	return components(c, edgesByWork, func(e seriesorder.Edge) bool { return StrongEdgeTypes[e.Type] })
}
