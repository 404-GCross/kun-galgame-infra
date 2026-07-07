package catalogsync

import (
	"strconv"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
)

// nyKey indexes a normalized title by its release year.
type nyKey struct {
	norm string
	year int
}

// runBangumi writes strict title-match PROBABLE bangumi refs for wiki games
// with no valid (audit-ok) bid. A match requires ALL of:
//   - a normalized-title equality on the non-empty side —
//     norm(name_ja_jp) == subject.name_norm OR
//     norm(name_zh_cn) == subject.name_cn_norm;
//   - equal release year (either side missing a year → no match);
//   - bidirectional uniqueness — the wiki row matches exactly one subject AND
//     that subject is matched by exactly one wiki row.
//
// The RHS is type=4 subjects NOT already occupied by an exact bid anchor.
// Fuzzy / edit-distance / LLM matching is explicitly out of scope (step 12).
//
// Normalization isomorphism (deviation from the doc's literal "lower(NFKC
// (trim))"): the wiki titles are normalized by the reconciler's SQL with the
// IDENTICAL expression the Silver layer uses for its generated columns —
// lower(normalize(col, NFKC)), NO trim. The Silver columns are not trimmed
// either, so reproducing the exact expression is byte-isomorphic by
// construction (and dodges the step-08 trap where strings.TrimSpace would eat
// the U+3000/U+00A0 the columns deliberately keep). Surrounding whitespace
// therefore blocks a match rather than forcing one — strictly conservative.
func (r *Reconciler) runBangumi() (BangumiStats, error) {
	var stats BangumiStats

	works, err := r.workIDMap()
	if err != nil {
		return stats, err
	}
	type4 := r.type4IDs()

	// The exact bid anchors occupy their subjects — never propose them again.
	auditOK := make(map[int64]struct{})
	for i := range r.games {
		if b := r.games[i].BID; b != nil {
			if _, ok := type4[*b]; ok {
				auditOK[*b] = struct{}{}
			}
		}
	}

	// RHS lookup: unoccupied type=4 subjects with a year, by normalized name.
	byJa := make(map[nyKey][]int64)
	byZh := make(map[nyKey][]int64)
	for _, s := range r.type4 {
		if s.Year == 0 {
			continue
		}
		if _, occupied := auditOK[s.ID]; occupied {
			continue
		}
		if s.NameNorm != "" {
			byJa[nyKey{s.NameNorm, s.Year}] = append(byJa[nyKey{s.NameNorm, s.Year}], s.ID)
		}
		if s.NameCNNorm != "" {
			byZh[nyKey{s.NameCNNorm, s.Year}] = append(byZh[nyKey{s.NameCNNorm, s.Year}], s.ID)
		}
	}

	// LHS pass: gather each eligible game's candidate subjects and, in the same
	// sweep, count how many games each subject attracts (the reverse direction
	// of the bidirectional-uniqueness test).
	type cand struct {
		workID int64
		subs   []int64
	}
	var lhs []cand
	subjCount := make(map[int64]int)
	for i := range r.games {
		g := &r.games[i]
		if g.Year == nil {
			continue
		}
		if g.BID != nil {
			if _, ok := type4[*g.BID]; ok {
				continue // has an exact bid anchor already
			}
		}
		workID, claimed := works[g.ID]
		if !claimed {
			continue
		}
		yr := *g.Year
		set := make(map[int64]struct{})
		if g.JaNorm != "" {
			for _, sid := range byJa[nyKey{g.JaNorm, yr}] {
				set[sid] = struct{}{}
			}
		}
		if g.ZhNorm != "" {
			for _, sid := range byZh[nyKey{g.ZhNorm, yr}] {
				set[sid] = struct{}{}
			}
		}
		if len(set) == 0 {
			continue
		}
		stats.CandidatesLHS++
		subs := make([]int64, 0, len(set))
		for sid := range set {
			subs = append(subs, sid)
			subjCount[sid]++
		}
		lhs = append(lhs, cand{workID: workID, subs: subs})
	}

	// Decide + write: accept only 1↔1 pairs.
	for _, c := range lhs {
		if len(c.subs) != 1 || subjCount[c.subs[0]] != 1 {
			stats.RejectedAmbiguous++
			continue
		}
		bangumiExternalID := strconv.FormatInt(c.subs[0], 10)
		if r.rejected.Has(model.EntityTypeWork, c.workID, sourceBangumi, bangumiExternalID) {
			stats.SkippedRejected++ // negative knowledge: never re-assert
			continue
		}
		stats.MatchedUnique++
		if r.dryRun {
			stats.RefsWritten++
			continue
		}
		written, err := repository.InsertRefIfAbsent(r.catalog, model.CatalogExternalRef{
			EntityType: model.EntityTypeWork,
			EntityID:   c.workID,
			SourceID:   sourceBangumi,
			ExternalID: bangumiExternalID,
			LinkKind:   model.LinkKindProbable,
			MatchedBy:  ruleTitleYear,
		})
		if err != nil {
			return stats, err
		}
		if written {
			stats.RefsWritten++
		} else {
			stats.Already++
		}
	}
	return stats, nil
}
