package tagcanon

import "sort"

const maxGroupSamples = 40

// topGroups digests the decided groups into the widest-first sample list (source
// count desc, then member count desc, then canonical name), capped.
func topGroups(groups []group) []GroupSample {
	out := make([]GroupSample, 0, len(groups))
	for _, g := range groups {
		out = append(out, GroupSample{
			Canonical: g.CanonicalName, Tier: g.Tier, Kind: g.Kind,
			Sources: g.sourceCount, Members: len(g.Members),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sources != out[j].Sources {
			return out[i].Sources > out[j].Sources
		}
		if out[i].Members != out[j].Members {
			return out[i].Members > out[j].Members
		}
		return out[i].Canonical < out[j].Canonical
	})
	if len(out) > maxGroupSamples {
		out = out[:maxGroupSamples]
	}
	return out
}

func sortByID(a []Absorption) {
	sort.Slice(a, func(i, j int) bool { return a[i].SourceID < a[j].SourceID })
}
