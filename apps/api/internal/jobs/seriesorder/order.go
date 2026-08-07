// Package seriesorder computes the two ordering facets a series membership row
// carries since wave 184 — catalog_series_member.position and .kind — from
// facts that already exist in the catalog: the members' release dates and the
// work↔work relation edges among them.
//
// It is deliberately ONE implementation shared by all three series lanes, which
// otherwise would each have grown their own: the dlsite reaper
// (internal/jobs/workseries) maintains the facets every pass, the derived
// builder (internal/jobs/derivedseries) sets them on the components it
// materializes, and cmd/backfill-series-order gives the pre-184 dlsite/curated
// rows their first values. Three copies of an ordering rule is three ways for
// the same series to sort differently depending on which job touched it last.
//
// The rules (refs/proj/184 拍死的设计 §6):
//
//   - position: earliest release date ascending, works with no dated release
//     last, ties broken by work id. 1-based; 0 stays the "not ordered" sentinel
//     and is never assigned by Assign.
//   - kind: the member's ROLE on the series-ish edges whose BOTH endpoints are
//     members of this same series — `a fandisc_of b` makes a a fandisc, `a
//     side_story_of b` makes a a side story, any other participation makes the
//     work a main entry. A member no edge touches gets the caller's fallback:
//     the derived lane says main (its members exist because an edge grouped
//     them), the pre-existing lanes say unknown rather than guess.
package seriesorder

import (
	"context"
	"sort"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// The series-ish relation types (catalog_relation_type ids, seed.go
// relationTypes()). This is the same edge set the derived builder clusters on,
// which is why it lives here rather than in either caller.
const (
	RelSequelOf    int64 = 2
	RelSideStoryOf int64 = 3
	RelFandiscOf   int64 = 4
	RelSameSeries  int64 = 7
)

// SeriesEdgeTypes is the {sequel, side story, fandisc, same series} subset —
// the edges that mean "these two works belong to one line", as opposed to
// adaptation / remake / crossover, which relate works ACROSS lines.
var SeriesEdgeTypes = []int64{RelSequelOf, RelSideStoryOf, RelFandiscOf, RelSameSeries}

// Assignment is one member's computed facets.
type Assignment struct {
	WorkID   int64
	Position int16
	Kind     int16
}

// Edge is one directed work↔work relation of a series-ish type.
type Edge struct {
	A    int64
	B    int64
	Type int64
}

// Facts is everything Assign needs, loaded ONCE for a whole batch of works
// rather than per series: a backfill over 736 series would otherwise issue
// thousands of round trips for data that fits in two queries.
type Facts struct {
	// releaseKey is the work's earliest dated release as a sortable
	// yyyymmdd-ish integer; a work with no dated release is absent.
	releaseKey map[int64]int64
	// edgesByWork indexes the series-ish edges by BOTH endpoints, so Assign
	// walks only the edges its own members touch.
	edgesByWork map[int64][]Edge
}

// LoadFacts reads the release dates and series-ish edges for a set of works.
// Both queries are scoped to the given ids, so a caller may batch as many
// series as it likes into one load.
func LoadFacts(ctx context.Context, db *gorm.DB, workIDs []int64) (*Facts, error) {
	f := &Facts{releaseKey: map[int64]int64{}, edgesByWork: map[int64][]Edge{}}
	if len(workIDs) == 0 {
		return f, nil
	}
	// Fuzzy dates (doc 17 R9): a release known only to the year sorts at the
	// START of that year, which is the conventional reading of "1999" against
	// "1999-08-27" and keeps an undated month from jumping a whole year.
	var dates []struct {
		WorkID int64 `gorm:"column:work_id"`
		Key    int64 `gorm:"column:key"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT work_id,
		       min(released_y::bigint * 10000 + coalesce(released_m, 0) * 100 + coalesce(released_d, 0)) AS key
		FROM catalog_release
		WHERE work_id IN ? AND deleted_at IS NULL AND released_y IS NOT NULL
		GROUP BY work_id`, workIDs).Scan(&dates).Error; err != nil {
		return nil, err
	}
	for _, d := range dates {
		f.releaseKey[d.WorkID] = d.Key
	}

	var edges []Edge
	if err := db.WithContext(ctx).Raw(`
		SELECT a_work_id AS a, b_work_id AS b, relation_type_id AS type
		FROM catalog_work_relation
		WHERE relation_type_id IN ? AND a_work_id IN ? AND b_work_id IN ?`,
		SeriesEdgeTypes, workIDs, workIDs).Scan(&edges).Error; err != nil {
		return nil, err
	}
	for _, e := range edges {
		f.edgesByWork[e.A] = append(f.edgesByWork[e.A], e)
		f.edgesByWork[e.B] = append(f.edgesByWork[e.B], e)
	}
	return f, nil
}

// Edges exposes the loaded edge index — the derived builder clusters connected
// components over exactly these edges, so it reads them from the same load
// instead of re-querying.
func (f *Facts) Edges() map[int64][]Edge { return f.edgesByWork }

// ReleaseKey returns a work's earliest dated release as a sortable integer and
// whether it has one; the derived namer picks its fallback name off the
// earliest-released member.
func (f *Facts) ReleaseKey(workID int64) (int64, bool) {
	k, ok := f.releaseKey[workID]
	return k, ok
}

// Assign computes position/kind for ONE series' member set. fallbackKind is the
// kind of a member the edge graph says nothing about (model.SeriesMemberKindMain
// for the derived lane, model.SeriesMemberKindUnknown elsewhere). The result is
// ordered by position, which makes it directly comparable with a query that
// reads the rows back in the same order.
func (f *Facts) Assign(workIDs []int64, fallbackKind int16) []Assignment {
	members := make(map[int64]struct{}, len(workIDs))
	for _, w := range workIDs {
		members[w] = struct{}{}
	}
	out := make([]Assignment, 0, len(members))
	for w := range members {
		out = append(out, Assignment{WorkID: w, Kind: f.kindOf(w, members, fallbackKind)})
	}
	// Undated works sort after every dated one; work id breaks every tie, so
	// the same member set always produces the same order.
	sort.Slice(out, func(i, j int) bool {
		ki, oki := f.releaseKey[out[i].WorkID]
		kj, okj := f.releaseKey[out[j].WorkID]
		switch {
		case oki && okj && ki != kj:
			return ki < kj
		case oki != okj:
			return oki
		default:
			return out[i].WorkID < out[j].WorkID
		}
	})
	for i := range out {
		out[i].Position = int16(i + 1)
	}
	return out
}

// kindOf reads the member's role off the edges whose both endpoints are in this
// series. Fandisc beats side story beats main, so a work asserted both ways by
// different sources lands on one deterministic answer rather than on whichever
// edge the scan happened to see last.
func (f *Facts) kindOf(workID int64, members map[int64]struct{}, fallbackKind int16) int16 {
	var fandisc, sideStory, edged bool
	for _, e := range f.edgesByWork[workID] {
		other := e.A
		if e.A == workID {
			other = e.B
		}
		if _, ok := members[other]; !ok {
			continue // an edge leaving this series says nothing about the role in it
		}
		edged = true
		if e.A != workID {
			continue // the ROLE types are directed: only the a-side carries it
		}
		switch e.Type {
		case RelFandiscOf:
			fandisc = true
		case RelSideStoryOf:
			sideStory = true
		}
	}
	switch {
	case fandisc:
		return model.SeriesMemberKindFandisc
	case sideStory:
		return model.SeriesMemberKindSideStory
	case edged:
		return model.SeriesMemberKindMain
	default:
		return fallbackKind
	}
}
