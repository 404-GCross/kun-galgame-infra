package wikirescue

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// seriesMinShared is how many of a wiki series' anchored members must land in
// the SAME catalog_series before the two are considered the same series. Name
// matching finds nothing here (the wiki names its series in Chinese, the
// dlsite-sourced catalog_series in Japanese), so membership overlap is the only
// available signal — and one shared member is far too weak, since catalog
// series are fine-grained and a single work belongs to several groupings.
const seriesMinShared = 2

// parkedSeriesIntro is a series description that could not be resolved onto a
// canonical catalog_series. The charter explicitly accepts parking here: the
// series facet is young (592 rows against the wiki's 146), so most wiki series
// have no counterpart yet and a later series wave can re-run this match.
type parkedSeriesIntro struct {
	SeriesID       int64   `json:"galgame_series_id"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	AnchoredWorks  int     `json:"anchored_works"`
	CandidateCS    []int64 `json:"candidate_catalog_series,omitempty"`
	CandidateStats []int   `json:"candidate_shared_members,omitempty"`
	Reason         string  `json:"reason"`
}

// stepSeriesIntro rescues galgame_series.description onto catalog_series_intro.
//
// Matching is STRUCTURAL, not by name: a wiki series' anchored member works are
// looked up in catalog_series_member, and the catalog series that shares at
// least seriesMinShared of them wins. A tie between two such series is
// ambiguous and parks rather than guessing.
func (r *Runner) stepSeriesIntro(ctx context.Context) (Stats, error) {
	st := Stats{Step: "f"}

	type wikiSeries struct {
		ID          int64
		Name        string
		Description string
	}
	var series []wikiSeries
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT id, name, description FROM galgame_series
		 WHERE coalesce(description, '') <> '' ORDER BY id`).Scan(&series).Error; err != nil {
		return st, fmt.Errorf("read galgame_series: %w", err)
	}
	st.Source = len(series)

	// Anchored members per wiki series.
	type memberRow struct {
		SeriesID      int64
		CatalogWorkID int64
	}
	var members []memberRow
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT series_id, catalog_work_id FROM galgame
		 WHERE series_id IS NOT NULL AND catalog_work_id IS NOT NULL AND catalog_work_id <> 0`).
		Scan(&members).Error; err != nil {
		return st, fmt.Errorf("read galgame series members: %w", err)
	}
	memberOf := map[int64][]int64{}
	for _, m := range members {
		memberOf[m.SeriesID] = append(memberOf[m.SeriesID], m.CatalogWorkID)
	}

	// work → the catalog series it belongs to.
	type csm struct {
		WorkID   int64
		SeriesID int64
	}
	var csms []csm
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT work_id, series_id FROM catalog_series_member`).Scan(&csms).Error; err != nil {
		return st, fmt.Errorf("read catalog_series_member: %w", err)
	}
	seriesOfWork := map[int64][]int64{}
	for _, m := range csms {
		seriesOfWork[m.WorkID] = append(seriesOfWork[m.WorkID], m.SeriesID)
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(series))
	parked := make([]parkedSeriesIntro, 0)
	seen := map[int64]struct{}{}
	for _, s := range series {
		works := memberOf[s.ID]
		shared := map[int64]int{}
		for _, w := range works {
			for _, cs := range seriesOfWork[w] {
				shared[cs]++
			}
		}
		best, bestN, tie := int64(0), 0, false
		for cs, n := range shared {
			switch {
			case n > bestN:
				best, bestN, tie = cs, n, false
			case n == bestN && cs != best:
				tie = true
			}
		}
		// The degenerate case the charter allows: a series with exactly one
		// anchored member that hits exactly one catalog series.
		soleMemberHit := len(works) == 1 && len(shared) == 1

		if bestN < seriesMinShared && !soleMemberHit {
			parked = append(parked, parkedSeriesIntro{
				SeriesID: s.ID, Name: s.Name, Description: s.Description,
				AnchoredWorks: len(works), CandidateCS: keysOf(shared), CandidateStats: valuesOf(shared),
				Reason: "no catalog_series shares enough member works",
			})
			continue
		}
		if tie {
			parked = append(parked, parkedSeriesIntro{
				SeriesID: s.ID, Name: s.Name, Description: s.Description,
				AnchoredWorks: len(works), CandidateCS: keysOf(shared), CandidateStats: valuesOf(shared),
				Reason: "ambiguous: several catalog_series share the same number of members",
			})
			continue
		}
		if _, dup := seen[best]; dup {
			parked = append(parked, parkedSeriesIntro{
				SeriesID: s.ID, Name: s.Name, Description: s.Description,
				AnchoredWorks: len(works), CandidateCS: []int64{best},
				Reason: "another wiki series already claimed this catalog_series in this run",
			})
			continue
		}
		seen[best] = struct{}{}
		rows = append(rows, []any{best, langZhHans, s.Description, r.wikiSrc, now, now})
	}
	st.Anchored = len(series) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)

	if err := r.park("f-series-intros", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}
	landed, err := insertReturning(r.catalog.WithContext(ctx), "catalog_series_intro",
		[]string{"series_id", "lang", "intro", "source_id", "created_at", "updated_at"}, "", rows)
	if err != nil {
		return st, err
	}
	st.Written = len(landed)
	return st, nil
}

// keysOf / valuesOf render a candidate map into two parallel, stably ordered
// slices for the parked artifact (JSON has no integer-keyed maps).
func keysOf(m map[int64]int) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func valuesOf(m map[int64]int) []int {
	ks := keysOf(m)
	out := make([]int, 0, len(ks))
	for _, k := range ks {
		out = append(out, m[k])
	}
	return out
}
