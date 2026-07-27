// Package tagedges projects the VNDB tag DAG (src_vndb.tags / tags_parents)
// onto the wiki tag vocabulary (galgame_tag), materializing galgame_tag_edge
// rows — the hierarchy that powers /v1/galgame/tags/multi expand=descendants
// and the tag-detail children block.
//
// Mapping: a VNDB tag joins a wiki tag when its PRIMARY English name equals
// (case-insensitive, trimmed) the wiki tag's name or one of its aliases — the
// wiki vocabulary was minted by vndbresolve, which stores the English VNDB
// name as an alias next to the Chinese tagMap name, so the alias join IS the
// provenance link. A normalized name claimed by two different wiki tags is
// dropped as ambiguous (counted, never guessed).
//
// Projection walks each mapped tag's parent chain upward (BFS over
// tags_parents) and emits an edge from the NEAREST mapped ancestor —
// compressing over unmapped intermediates so "A → (unmapped B) → C" still
// yields A → C. Parents that are meta grouping nodes (searchable=false or
// applicable=false, e.g. VNDB's "Type"/"Theme" roots) never become edge
// parents; the walk continues THROUGH them. That is also why the DAG route is
// semantically safe where string heuristics were not: "No Romance Plot" hangs
// under the meta node "Type", not under "Romance", so expanding 恋爱 can never
// pull it in.
//
// Discipline (repo backfill conventions):
//   - Dry-run is the DEFAULT; --apply writes. The decided plan (counters +
//     samples) is identical in both modes.
//   - Reconciles the source="vndb" subset ONLY: missing edges are inserted
//     (ON CONFLICT DO NOTHING), stale ones are pruned; user-curated rows
//     (source="") are never touched. Idempotent — a second --apply is a no-op.
//   - Re-run after every src_vndb dump refresh (cmd/ingest-vndb); the edges
//     are otherwise static.
package tagedges

import (
	"context"
	"sort"
	"strings"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// maxDepth bounds the upward walk per child (the VNDB tag DAG is ~4 levels
// deep; 32 is a cycle backstop, not a tuning knob).
const maxDepth = 32

// maxSamples caps the dry-run example edges collected for logging / tests.
const maxSamples = 12

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes).
	Apply bool
}

// Sample is one planned edge for dry-run logging / test assertions.
type Sample struct {
	Parent string
	Child  string
	Depth  int // 1 = direct VNDB edge; >1 = compressed over unmapped intermediates
}

// Stats is the run digest — identical in dry and apply modes except
// Inserted/Pruned, which only move under --apply.
type Stats struct {
	WikiTags     int // wiki vocabulary size
	WikiAliases  int
	VndbTags     int // src_vndb.tags rows
	VndbEdges    int // src_vndb.tags_parents rows
	Mapped       int // VNDB tags joined to a wiki tag
	Ambiguous    int // normalized names claimed by ≥2 wiki tags (dropped)
	Planned      int // desired vndb-sourced edge set size
	Compressed   int // planned edges with depth > 1
	PlannedNew   int // planned - already present
	PlannedPrune int // present vndb-sourced edges not in the plan
	Inserted     int64
	Pruned       int64
	Samples      []Sample
}

// edge is one planned parent→child pair (wiki tag ids).
type edge struct {
	parent, child int
	depth         int
}

// Run computes the desired edge set and reconciles galgame_tag_edge
// (source="vndb") against it. wikiDB holds galgame_tag / galgame_tag_alias /
// galgame_tag_edge; vndbDB holds the src_vndb schema (the same DB in
// production, split pools in local dev).
func Run(ctx context.Context, wikiDB, vndbDB *gorm.DB, opts Opts) (Stats, error) {
	var st Stats

	// ── 1. wiki vocabulary: normalized name/alias → wiki tag id ──
	type wikiTag struct {
		ID   int
		Name string
	}
	var wikiTags []wikiTag
	if err := wikiDB.WithContext(ctx).Table("galgame_tag").
		Select("id, name").Scan(&wikiTags).Error; err != nil {
		return st, err
	}
	st.WikiTags = len(wikiTags)

	type wikiAlias struct {
		GalgameTagID int
		Name         string
	}
	var wikiAliases []wikiAlias
	if err := wikiDB.WithContext(ctx).Table("galgame_tag_alias").
		Select("galgame_tag_id, name").Scan(&wikiAliases).Error; err != nil {
		return st, err
	}
	st.WikiAliases = len(wikiAliases)

	// norm(name) → wiki id; collisions across DIFFERENT wiki tags are dropped
	// (ambiguous). A tag's own name + aliases colliding is fine.
	nameToWiki := make(map[string]int, len(wikiTags)+len(wikiAliases))
	ambiguous := make(map[string]bool)
	claim := func(name string, id int) {
		n := norm(name)
		if n == "" || ambiguous[n] {
			return
		}
		if prev, ok := nameToWiki[n]; ok && prev != id {
			ambiguous[n] = true
			delete(nameToWiki, n)
			return
		}
		nameToWiki[n] = id
	}
	for _, t := range wikiTags {
		claim(t.Name, t.ID)
	}
	for _, a := range wikiAliases {
		claim(a.Name, a.GalgameTagID)
	}
	st.Ambiguous = len(ambiguous)

	wikiName := make(map[int]string, len(wikiTags))
	for _, t := range wikiTags {
		wikiName[t.ID] = t.Name
	}

	// ── 2. VNDB vocabulary + DAG (src_vndb) ──
	type vndbTag struct {
		ID         string
		Name       string
		Searchable bool
		Applicable bool
	}
	var vtags []vndbTag
	if err := vndbDB.WithContext(ctx).Table("src_vndb.tags").
		Select("id, name, searchable, applicable").Scan(&vtags).Error; err != nil {
		return st, err
	}
	st.VndbTags = len(vtags)

	type vndbEdge struct {
		ID     string // child
		Parent string
	}
	var vedges []vndbEdge
	if err := vndbDB.WithContext(ctx).Table("src_vndb.tags_parents").
		Select("id, parent").Scan(&vedges).Error; err != nil {
		return st, err
	}
	st.VndbEdges = len(vedges)

	// ── 3. join: VNDB gid → wiki id (primary English name only) ──
	info := make(map[string]vndbTag, len(vtags))
	gidToWiki := make(map[string]int, len(vtags))
	for _, v := range vtags {
		info[v.ID] = v
		if wid, ok := nameToWiki[norm(v.Name)]; ok {
			gidToWiki[v.ID] = wid
		}
	}
	st.Mapped = len(gidToWiki)

	parentsOf := make(map[string][]string, len(vedges))
	for _, e := range vedges {
		parentsOf[e.ID] = append(parentsOf[e.ID], e.Parent)
	}

	// ── 4. project: nearest mapped + non-meta ancestor per mapped child ──
	desired := make(map[[2]int]edge)
	for gid, childWiki := range gidToWiki {
		visited := map[string]bool{gid: true}
		frontier := parentsOf[gid]
		for depth := 1; depth <= maxDepth && len(frontier) > 0; depth++ {
			var next []string
			for _, pgid := range frontier {
				if visited[pgid] {
					continue
				}
				visited[pgid] = true
				pv := info[pgid]
				parentWiki, mapped := gidToWiki[pgid]
				if mapped && pv.Searchable && pv.Applicable && parentWiki != childWiki {
					key := [2]int{parentWiki, childWiki}
					if prev, ok := desired[key]; !ok || depth < prev.depth {
						desired[key] = edge{parent: parentWiki, child: childWiki, depth: depth}
					}
					continue // nearest mapped ancestor found on this path — stop here
				}
				// Unmapped or meta node: compress through it.
				next = append(next, parentsOf[pgid]...)
			}
			frontier = next
		}
	}
	st.Planned = len(desired)
	for _, e := range desired {
		if e.depth > 1 {
			st.Compressed++
		}
	}

	// Deterministic samples: order by parent name, then child name.
	planned := make([]edge, 0, len(desired))
	for _, e := range desired {
		planned = append(planned, e)
	}
	sort.Slice(planned, func(i, j int) bool {
		if wikiName[planned[i].parent] != wikiName[planned[j].parent] {
			return wikiName[planned[i].parent] < wikiName[planned[j].parent]
		}
		return wikiName[planned[i].child] < wikiName[planned[j].child]
	})
	for _, e := range planned {
		if len(st.Samples) >= maxSamples {
			break
		}
		st.Samples = append(st.Samples, Sample{
			Parent: wikiName[e.parent], Child: wikiName[e.child], Depth: e.depth,
		})
	}

	// ── 5. reconcile the source="vndb" subset ──
	var existing []model.GalgameTagEdge
	if err := wikiDB.WithContext(ctx).
		Where("source = ?", "vndb").Find(&existing).Error; err != nil {
		return st, err
	}
	existingSet := make(map[[2]int]bool, len(existing))
	var stale []model.GalgameTagEdge
	for _, e := range existing {
		key := [2]int{e.ParentID, e.ChildID}
		existingSet[key] = true
		if _, ok := desired[key]; !ok {
			stale = append(stale, e)
		}
	}
	var missing []model.GalgameTagEdge
	for key := range desired {
		if !existingSet[key] {
			missing = append(missing, model.GalgameTagEdge{
				ParentID: key[0], ChildID: key[1], Source: "vndb",
			})
		}
	}
	st.PlannedNew = len(missing)
	st.PlannedPrune = len(stale)

	if !opts.Apply {
		return st, nil
	}

	if len(missing) > 0 {
		res := wikiDB.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			CreateInBatches(missing, 500)
		if res.Error != nil {
			return st, res.Error
		}
		st.Inserted = res.RowsAffected
	}
	for _, e := range stale {
		res := wikiDB.WithContext(ctx).
			Where("parent_id = ? AND child_id = ? AND source = ?", e.ParentID, e.ChildID, "vndb").
			Delete(&model.GalgameTagEdge{})
		if res.Error != nil {
			return st, res.Error
		}
		st.Pruned += res.RowsAffected
	}
	return st, nil
}

// norm folds a tag name for the join: trim + lower. The join key is English
// VNDB names (ASCII); anything fancier (NFKC etc.) belongs to the catalog
// tag-canonicalization lane, not here.
func norm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
