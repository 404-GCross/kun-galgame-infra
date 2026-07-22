package tagcanon

import (
	"sort"

	"api/internal/platform/catalog/model"
)

// group is one canonical tag decided this wave: a norm shared by ≥2 distinct
// sources. Members is one entry per (source, original-name) — several members
// may share a source (Galgame/galgame both under bangumi), each getting its own
// map row.
type group struct {
	Norm          string
	CanonicalName string
	Tier          int16
	Kind          int16
	Members       []vocabEntry
	sourceCount   int
}

// buildGroups folds the non-junk vocabulary on Norm and keeps the norms spanning
// ≥2 distinct sources (doc 74 §确定性预配-3). Groups are returned sorted by Norm
// so a --limit small-sample apply is deterministic. Junk entries are skipped
// entirely (never grouped, never mapped).
func buildGroups(vocab []vocabEntry) []group {
	byNorm := map[string][]vocabEntry{}
	for _, e := range vocab {
		if e.Junk {
			continue
		}
		byNorm[e.Norm] = append(byNorm[e.Norm], e)
	}
	out := make([]group, 0)
	for norm, members := range byNorm {
		srcs := map[int16]struct{}{}
		for _, m := range members {
			srcs[m.SourceID] = struct{}{}
		}
		if len(srcs) < 2 {
			continue // single-source name — 70b's domain, no row this wave
		}
		// Deterministic member order (source, then name) for stable map writes.
		sort.Slice(members, func(i, j int) bool {
			if members[i].SourceID != members[j].SourceID {
				return members[i].SourceID < members[j].SourceID
			}
			return members[i].Name < members[j].Name
		})
		g := group{
			Norm:          norm,
			CanonicalName: pickCanonicalName(members),
			Tier:          model.TagTierCore, // cross-source ≥2 = core (user ruling)
			Kind:          model.TagKindContent,
			Members:       members,
			sourceCount:   len(srcs),
		}
		if isMeta(norm) {
			g.Kind = model.TagKindMeta
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Norm < out[j].Norm })
	return out
}

// pickCanonicalName chooses the group's display form deterministically: the
// original string that the MOST distinct sources agree on, tie-broken by the
// highest total usage, then lexicographically smallest. (Galgame vs galgame:
// galgame is in vndb+bangumi = 2 sources, Galgame only bangumi = 1, so galgame
// wins — the form the sources agree on.)
func pickCanonicalName(members []vocabEntry) string {
	type agg struct {
		sources map[int16]struct{}
		usage   int
	}
	byForm := map[string]*agg{}
	for _, m := range members {
		a := byForm[m.Name]
		if a == nil {
			a = &agg{sources: map[int16]struct{}{}}
			byForm[m.Name] = a
		}
		a.sources[m.SourceID] = struct{}{}
		a.usage += m.Usage
	}
	best := ""
	var bestSrc, bestUsage int
	for form, a := range byForm {
		src := len(a.sources)
		switch {
		case best == "",
			src > bestSrc,
			src == bestSrc && a.usage > bestUsage,
			src == bestSrc && a.usage == bestUsage && form < best:
			best, bestSrc, bestUsage = form, src, a.usage
		}
	}
	return best
}

// Bucket is one usage-threshold count of the single-source distribution.
type Bucket struct {
	GE100 int
	GE50  int
	GE20  int
	GE10  int
	Total int // all single-source non-junk names of the source
}

// NameUsage is one (name, usage) sample.
type NameUsage struct {
	Name  string
	Usage int
}

// SourceDist is a source's single-source usage distribution + top samples — the
// input for pinning the single-source core-tier threshold (doc 74 §统计交付).
type SourceDist struct {
	SourceID int16
	Key      string
	Buckets  Bucket
	Top      []NameUsage // highest-usage single-source names first, capped
}

const maxTopSamples = 50

// singleSourceDist computes, per source, the usage distribution over names that
// did NOT converge into a cross-source group (norm spanning <2 sources) and are
// not junk — the population a single-source core threshold would select from.
func singleSourceDist(vocab []vocabEntry, groups []group, srcKeys map[int16]string) []SourceDist {
	inGroup := map[string]struct{}{}
	for _, g := range groups {
		inGroup[g.Norm] = struct{}{}
	}
	perSrc := map[int16][]NameUsage{}
	for _, e := range vocab {
		if e.Junk {
			continue
		}
		if _, grouped := inGroup[e.Norm]; grouped {
			continue
		}
		perSrc[e.SourceID] = append(perSrc[e.SourceID], NameUsage{Name: e.Name, Usage: e.Usage})
	}
	ids := make([]int16, 0, len(perSrc))
	for id := range perSrc {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	out := make([]SourceDist, 0, len(ids))
	for _, id := range ids {
		names := perSrc[id]
		d := SourceDist{SourceID: id, Key: srcKeys[id]}
		for _, n := range names {
			d.Buckets.Total++
			switch {
			case n.Usage >= 100:
				d.Buckets.GE100++
				fallthrough
			case n.Usage >= 50:
				d.Buckets.GE50++
				fallthrough
			case n.Usage >= 20:
				d.Buckets.GE20++
				fallthrough
			case n.Usage >= 10:
				d.Buckets.GE10++
			}
		}
		sort.Slice(names, func(i, j int) bool {
			if names[i].Usage != names[j].Usage {
				return names[i].Usage > names[j].Usage
			}
			return names[i].Name < names[j].Name
		})
		if len(names) > maxTopSamples {
			names = names[:maxTopSamples]
		}
		d.Top = names
		out = append(out, d)
	}
	return out
}
