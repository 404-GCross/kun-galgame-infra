package getchuchars

import (
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Match levels, recorded on every write so a later audit can ask "which rule
// produced this link" without re-deriving it.
const (
	MatchDisplayName = "name"
	MatchAlias       = "alias"
	MatchReading     = "reading"
)

var reSpace = regexp.MustCompile(`[[:space:]　]`)

// normKey is the one normalization both sides go through: NFKC (so fullwidth
// and halfwidth forms meet), all whitespace removed (Getchu writes "九條 都"
// with an ideographic space where the catalog may hold "九條都"), lowercased.
//
// Deliberately nothing more. Kana folding or romaji transliteration would widen
// the net past what a within-work roster can safely disambiguate.
func normKey(s string) string {
	return strings.ToLower(norm.NFKC.String(reSpace.ReplaceAllString(s, "")))
}

// rosterIndex is one work's name forms, keyed for lookup.
type rosterIndex struct {
	byName  map[string][]int64 // normalized display name → character ids
	byAlias map[string][]int64
}

func buildIndex(rows []rosterRow) map[string]*rosterIndex {
	out := map[string]*rosterIndex{}
	for _, r := range rows {
		idx := out[r.GetchuID]
		if idx == nil {
			idx = &rosterIndex{byName: map[string][]int64{}, byAlias: map[string][]int64{}}
			out[r.GetchuID] = idx
		}
		if r.KeyName != "" {
			idx.byName[r.KeyName] = appendUnique(idx.byName[r.KeyName], r.CharacterID)
		}
		if r.KeyAlias != "" {
			idx.byAlias[r.KeyAlias] = appendUnique(idx.byAlias[r.KeyAlias], r.CharacterID)
		}
	}
	return out
}

func appendUnique(xs []int64, v int64) []int64 {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// MatchStats reports how a run's matching went. Every crawled character lands
// in exactly one bucket, so the numbers always add up to the input.
type MatchStats struct {
	Input        int
	NoWork       int // the getchu item has no anchored catalog work
	Matched      int
	ByName       int
	ByAlias      int
	ByReading    int
	Ambiguous    int // one name, several roster characters
	Collided     int // several rows of the SAME getchu item claim one character
	Deduped      int // the same character described by several EDITIONS of one work
	NoNameInWork int // roster exists, no form matched
}

// match resolves crawled characters against the roster index.
//
// Ambiguity is resolved by DROPPING, never by picking. Within one work the
// plausible collisions are twins, siblings sharing a surname, and a nickname a
// relative also answers to — precisely the cases where a wrong link looks
// right.
func match(chars []getchuChar, idx map[string]*rosterIndex) ([]Candidate, MatchStats) {
	st := MatchStats{Input: len(chars)}
	type hit struct {
		cand Candidate
		ok   bool
	}
	hits := make([]hit, len(chars))
	claims := map[int64]int{} // character id → how many getchu rows claimed it

	for i, c := range chars {
		ri := idx[c.GetchuID]
		if ri == nil {
			st.NoWork++
			continue
		}
		nk, rk := normKey(c.Name), normKey(c.Reading)

		var ids []int64
		var by string
		switch {
		case len(ri.byName[nk]) > 0:
			ids, by = ri.byName[nk], MatchDisplayName
		case len(ri.byAlias[nk]) > 0:
			ids, by = ri.byAlias[nk], MatchAlias
		case rk != "" && len(ri.byName[rk]) > 0:
			ids, by = ri.byName[rk], MatchReading
		case rk != "" && len(ri.byAlias[rk]) > 0:
			ids, by = ri.byAlias[rk], MatchReading
		default:
			st.NoNameInWork++
			continue
		}
		if len(ids) != 1 {
			st.Ambiguous++
			continue
		}
		hits[i] = hit{Candidate{
			CharacterID: ids[0], GetchuID: c.GetchuID, Ordinal: c.Ordinal,
			Name: c.Name, Profile: c.Profile, Attrs: c.Attrs, MatchedBy: by,
		}, true}
		claims[ids[0]]++
	}

	// Group by catalog character to separate the two reasons several crawled
	// rows can point at one.
	byChar := map[int64][]Candidate{}
	for _, h := range hits {
		if h.ok {
			byChar[h.cand.CharacterID] = append(byChar[h.cand.CharacterID], h.cand)
		}
	}

	out := make([]Candidate, 0, len(chars))
	for _, group := range byChar {
		if len(group) > 1 {
			// Several rows of the SAME getchu item claiming one character is
			// real ambiguity — drop them. Rows from DIFFERENT items are the
			// same product's 限定版 / 通常版 / DL版, whose rosters are
			// identical by construction: 3,108 works carry more than one getchu
			// id, and treating those as ambiguity threw away a third of the
			// match set on the first real run.
			sameItem := true
			for _, c := range group[1:] {
				if c.GetchuID != group[0].GetchuID {
					sameItem = false
					break
				}
			}
			if sameItem {
				st.Collided += len(group)
				continue
			}
			st.Deduped += len(group) - 1
			group = []Candidate{richest(group)}
		}
		h := struct{ cand Candidate }{group[0]}
		st.Matched++
		switch h.cand.MatchedBy {
		case MatchDisplayName:
			st.ByName++
		case MatchAlias:
			st.ByAlias++
		case MatchReading:
			st.ByReading++
		}
		out = append(out, h.cand)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CharacterID != out[j].CharacterID {
			return out[i].CharacterID < out[j].CharacterID
		}
		return out[i].Ordinal < out[j].Ordinal
	})
	return out, st
}

// richest picks which edition's copy of a character to keep: the longest
// profile wins (editions differ mainly by one carrying fuller text), with
// (getchu_id, ordinal) as the tie-break so the choice is reproducible rather
// than map-iteration order.
func richest(group []Candidate) Candidate {
	best := group[0]
	for _, c := range group[1:] {
		switch {
		case len(c.Profile) > len(best.Profile):
			best = c
		case len(c.Profile) == len(best.Profile):
			if c.GetchuID < best.GetchuID || (c.GetchuID == best.GetchuID && c.Ordinal < best.Ordinal) {
				best = c
			}
		}
	}
	return best
}
