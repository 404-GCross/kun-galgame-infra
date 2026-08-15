package tagcanon

import (
	"sort"

	"api/internal/platform/catalog/model"
)

type group struct {
	Norm          string
	CanonicalName string
	Tier          int16
	Kind          int16
	Sexual        bool
	Members       []vocabEntry
	sourceCount   int
}

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
			continue
		}
		sort.Slice(members, func(i, j int) bool {
			if members[i].SourceID != members[j].SourceID {
				return members[i].SourceID < members[j].SourceID
			}
			return members[i].Name < members[j].Name
		})
		g := group{
			Norm:          norm,
			CanonicalName: pickCanonicalName(members),
			Tier:          model.TagTierCore,
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

type Bucket struct {
	GE100 int
	GE50  int
	GE20  int
	GE10  int
	Total int
}

type NameUsage struct {
	Name  string
	Usage int
}

type SourceDist struct {
	SourceID int16
	Key      string
	Buckets  Bucket
	Top      []NameUsage
}

const maxTopSamples = 50

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
