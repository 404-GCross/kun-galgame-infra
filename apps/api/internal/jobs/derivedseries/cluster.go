package derivedseries

import (
	"sort"

	"api/internal/jobs/seriesorder"
)

const GiantSize = 30

var StrongEdgeTypes = map[int64]bool{
	seriesorder.RelSequelOf:   true,
	seriesorder.RelSameSeries: true,
}

type component []int64

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
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func splitGiant(c component, edgesByWork map[int64][]seriesorder.Edge) []component {
	return components(c, edgesByWork, func(e seriesorder.Edge) bool { return StrongEdgeTypes[e.Type] })
}
