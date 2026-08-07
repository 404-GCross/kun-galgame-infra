// Package derivedseries builds series that no upstream published, out of facts
// the catalog already holds (wave 184, refs/proj/184).
//
// The catalog knows 22,860 work↔work relation edges but only 2,780 works sat in
// a series, because the only series vocabularies it mirrored were dlsite's and
// the wiki's hand-curated one. The edges already say which works belong
// together — a connected component over {sequel_of, side_story_of, fandisc_of,
// same_series} IS a series line — so this job materializes those components
// under their own source (`derived`, id 18) rather than leaving the grouping
// implicit in an edge table no read face walks.
//
// Three rules keep it from overwriting anyone (拍死的设计 §2/§3/§4):
//
//   - A component that touches ANY existing dlsite or curated series is not
//     built. The machine never re-groups works a source or a human already
//     grouped; the component goes to a worklist instead, for a curator to enrich
//     the existing series with if they want.
//   - A component of 30+ works is a franchise blob, not a series — one weak edge
//     welds two unrelated lines together permanently. It is re-clustered on the
//     strong edges alone, and whatever is still 30+ after that is left to the
//     worklist rather than published under a name no rule could pick.
//   - Identity is `comp:<smallest live member work id>`, and the job is a REAPER
//     over that key: members insert-absent / delete-stale, a series that drops
//     below 2 members is deleted whole, and display_name is recomputed every
//     pass because the builder is the lane's only writer (nobody renames a
//     derived series — the edit face's curatedOnly guard refuses).
//
// The identity moves when the graph does: two components merging changes the
// minimum id, so the old series is deleted and a new one created rather than
// mutated. That is accepted — a merged component IS a different grouping, and
// the alternative (chasing identity across merges) buys a stable id for a
// series whose membership changed underneath it anyway.
package derivedseries

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"api/internal/jobs/seriesorder"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// derivedSourceKey is the machine-inference lane (catalog_source id 18).
const derivedSourceKey = "derived"

// ownedSourceKeys are the lanes whose groupings this job must never touch or
// duplicate: the dlsite mirror and the human curated lane.
var ownedSourceKeys = []string{"dlsite", "curated"}

// Opts configures a run. Apply=false is the repo's dry-run default; DSN is
// REQUIRED and never guessed. Worklist is an optional jsonl path for the
// components this run refused to build.
type Opts struct {
	Apply    bool
	DSN      string
	Receipts string
	Worklist string
}

// Stats reports a run. Dry and apply plan identically; the write counters are a
// forecast in dry and a measurement in apply, and a second apply reports zero
// for every one of them.
type Stats struct {
	Works      int // live works touched by at least one series-ish edge
	Edges      int
	Components int
	// SkippedOverlap / SkippedGiant are the two refusals; both land on the
	// worklist rather than silently vanishing.
	SkippedOverlap int
	SkippedGiant   int
	// SkippedNoName is a component whose members are ALL title-less: there is
	// no honest name to publish it under, and inventing one ("comp:1234") would
	// put a machine identifier on a reader's screen.
	SkippedNoName int
	GiantsSplit   int // oversized components the strong-edge pass rescued
	Eligible      int // components this run wants materialized
	MembersWanted int

	SeriesCreated int
	SeriesRenamed int
	SeriesDeleted int
	MembersAdded  int
	MembersStale  int
	OrderChanged  int
	TouchedWorks  int

	NamedByPrefix   int
	NamedByFallback int
}

// receipt is one built component.
type receipt struct {
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`
	NamedBy     string `json:"named_by"` // prefix | fallback
	SeriesID    int64  `json:"series_id,omitempty"`
	Members     []struct {
		WorkID   int64  `json:"work_id"`
		Position int16  `json:"position"`
		Kind     int16  `json:"kind"`
		Title    string `json:"title"`
	} `json:"members"`
}

// worklistEntry is one component this run refused to build.
type worklistEntry struct {
	Reason     string  `json:"reason"` // overlap | giant | no_name
	ExternalID string  `json:"external_id"`
	WorkIDs    []int64 `json:"work_ids"`
	Size       int     `json:"size"`
	// HitSeriesIDs is the existing dlsite/curated series the component collides
	// with (overlap only) — the pointer a curator needs to enrich them.
	HitSeriesIDs []int64 `json:"hit_series_ids,omitempty"`
}

// candidate is a component this run wants to materialize.
type candidate struct {
	externalID string
	works      []int64
	name       string
	namedBy    string
}

// Run builds the derived lane against the DSN.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess")
	}
	db, err := gorm.Open(postgres.Open(opts.DSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	return RunWithDB(ctx, db, opts)
}

// RunWithDB is Run against an already-open pool (the tests' entry point).
func RunWithDB(ctx context.Context, db *gorm.DB, opts Opts) (*Stats, error) {
	derivedSrc, err := resolveSource(ctx, db, derivedSourceKey)
	if err != nil {
		return nil, err
	}
	st := &Stats{}

	// Only works that carry a series-ish edge can be in a component, so the
	// live-work scan is scoped to the edge endpoints rather than to the whole
	// catalogue — the difference is ~17k rows against ~100k.
	var rawEdges []seriesorder.Edge
	if err := db.WithContext(ctx).Raw(`
		SELECT r.a_work_id AS a, r.b_work_id AS b, r.relation_type_id AS type
		FROM catalog_work_relation r
		JOIN catalog_work wa ON wa.id = r.a_work_id AND wa.deleted_at IS NULL
		JOIN catalog_work wb ON wb.id = r.b_work_id AND wb.deleted_at IS NULL
		WHERE r.relation_type_id IN ?`, seriesorder.SeriesEdgeTypes).Scan(&rawEdges).Error; err != nil {
		return nil, fmt.Errorf("load edges: %w", err)
	}
	st.Edges = len(rawEdges)
	edgesByWork := map[int64][]seriesorder.Edge{}
	workSet := map[int64]struct{}{}
	for _, e := range rawEdges {
		edgesByWork[e.A] = append(edgesByWork[e.A], e)
		edgesByWork[e.B] = append(edgesByWork[e.B], e)
		workSet[e.A] = struct{}{}
		workSet[e.B] = struct{}{}
	}
	works := make([]int64, 0, len(workSet))
	for w := range workSet {
		works = append(works, w)
	}
	st.Works = len(works)

	owned, err := ownedMembership(ctx, db)
	if err != nil {
		return nil, err
	}

	var wl *os.File
	if opts.Worklist != "" {
		if wl, err = os.Create(opts.Worklist); err != nil {
			return nil, fmt.Errorf("worklist: %w", err)
		}
		defer wl.Close()
	}
	wlEnc := json.NewEncoder(wl)
	emit := func(e worklistEntry) error {
		if wl == nil {
			return nil
		}
		return wlEnc.Encode(e)
	}

	// ── cluster, refuse, split ───────────────────────────────────────────────
	comps := components(works, edgesByWork, nil)
	st.Components = len(comps)
	var kept []component
	for _, c := range comps {
		// Overlap is checked on the WHOLE component, before any split: a
		// component that reaches into an existing series is that series'
		// business no matter how it would have been carved up.
		if hits := overlapping(c, owned); len(hits) > 0 {
			st.SkippedOverlap++
			if err := emit(worklistEntry{Reason: "overlap", ExternalID: externalID(c),
				WorkIDs: c, Size: len(c), HitSeriesIDs: hits}); err != nil {
				return nil, fmt.Errorf("worklist: %w", err)
			}
			continue
		}
		if len(c) < GiantSize {
			kept = append(kept, c)
			continue
		}
		var rescued []component
		for _, sub := range splitGiant(c, edgesByWork) {
			if len(sub) >= GiantSize {
				continue // still a blob after the strong-edge pass
			}
			rescued = append(rescued, sub)
		}
		st.GiantsSplit++
		kept = append(kept, rescued...)
		// The parent is reported whole either way: what the split rescued is
		// visible in the receipts, and what it did not is exactly the residue a
		// human has to look at.
		st.SkippedGiant++
		if err := emit(worklistEntry{Reason: "giant", ExternalID: externalID(c),
			WorkIDs: c, Size: len(c)}); err != nil {
			return nil, fmt.Errorf("worklist: %w", err)
		}
	}

	// ── name and order ───────────────────────────────────────────────────────
	var allMembers []int64
	for _, c := range kept {
		allMembers = append(allMembers, c...)
	}
	facts, err := seriesorder.LoadFacts(ctx, db, allMembers)
	if err != nil {
		return nil, fmt.Errorf("load ordering facts: %w", err)
	}
	titles, err := workTitles(ctx, db, allMembers)
	if err != nil {
		return nil, err
	}
	want := make(map[string]*candidate, len(kept))
	for _, c := range kept {
		names := make([]string, 0, len(c))
		for _, w := range c {
			names = append(names, titles[w])
		}
		// The fallback title comes off the member Assign put first, which is the
		// earliest-released one (work id breaking the tie) — the same ordering
		// the members themselves are published in.
		ordered := facts.Assign(c, model.SeriesMemberKindMain)
		name, byPrefix := nameComponent(names, titles[ordered[0].WorkID])
		if name == "" {
			// Every member is title-less: nothing honest to call this group.
			st.SkippedNoName++
			if err := emit(worklistEntry{Reason: "no_name", ExternalID: externalID(c),
				WorkIDs: c, Size: len(c)}); err != nil {
				return nil, fmt.Errorf("worklist: %w", err)
			}
			continue
		}
		cand := &candidate{externalID: externalID(c), works: c, name: name, namedBy: "fallback"}
		if byPrefix {
			cand.namedBy = "prefix"
			st.NamedByPrefix++
		} else {
			st.NamedByFallback++
		}
		want[cand.externalID] = cand
		st.MembersWanted += len(c)
	}
	st.Eligible = len(want)

	touched, err := reconcile(ctx, db, derivedSrc, want, facts, titles, opts, st)
	if err != nil {
		return nil, err
	}
	if opts.Apply && len(touched) > 0 {
		if err := repository.TouchWorks(ctx, db, touched); err != nil {
			return nil, fmt.Errorf("touch works: %w", err)
		}
		st.TouchedWorks = len(dedupe(touched))
	}
	return st, nil
}

// externalID is the component's identity: its smallest member's work id. Stable
// as long as the component is, which is the point — it moves only when the
// graph really merged or split the group.
func externalID(c component) string { return fmt.Sprintf("comp:%d", c[0]) }

// overlapping returns the existing dlsite/curated series a component collides
// with, deduplicated and ordered.
func overlapping(c component, owned map[int64][]int64) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	for _, w := range c {
		for _, s := range owned[w] {
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// ownedMembership maps work → the dlsite/curated series holding it.
func ownedMembership(ctx context.Context, db *gorm.DB) (map[int64][]int64, error) {
	var rows []struct {
		WorkID   int64 `gorm:"column:work_id"`
		SeriesID int64 `gorm:"column:series_id"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT m.work_id, m.series_id
		FROM catalog_series_member m
		JOIN catalog_series s ON s.id = m.series_id
		JOIN catalog_source src ON src.id = s.source_id
		WHERE src.key IN ?`, ownedSourceKeys).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load existing memberships: %w", err)
	}
	out := make(map[int64][]int64, len(rows))
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], r.SeriesID)
	}
	return out, nil
}

func workTitles(ctx context.Context, db *gorm.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID          int64  `gorm:"column:id"`
		DisplayName string `gorm:"column:display_name"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT id, display_name FROM catalog_work WHERE id IN ?`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load work titles: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.DisplayName
	}
	return out, nil
}

func resolveSource(ctx context.Context, db *gorm.DB, key string) (int16, error) {
	var id int16
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).
		Scan(&id).Error; err != nil {
		return 0, err
	}
	if id == 0 {
		return 0, fmt.Errorf("catalog_source has no %q row (run the catalog migration/seed first)", key)
	}
	return id, nil
}

func dedupe(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
