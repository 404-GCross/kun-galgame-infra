// public_label_graph_test.go — wave 188: the label relation GRAPH face
// (labels/{id}/relation-graph). Integration against the real catalog schema
// (service_test.go TestMain).
//
// The invariants pinned here are the ones a picture depends on: the walk
// reaches the whole family and terminates on a cyclic graph, the bounds bite
// breadth-first, a merged-away label is invisible, and a MIRRORED fact renders
// exactly once.
package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// cleanLabelGraphTables truncates the relation table cleanTables does not
// cover (it hangs off catalog_label by FK, so it is emptied by the CASCADE in
// practice — truncating it explicitly keeps this suite independent of that).
func cleanLabelGraphTables(t *testing.T) {
	t.Helper()
	if err := testDB.Exec("TRUNCATE catalog_label_relation RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate catalog_label_relation: %v", err)
	}
}

// mkLabel inserts one label (optionally with a logo) and returns its id.
func mkLabel(t *testing.T, name, logoHash string) int64 {
	t.Helper()
	lbl := &model.CatalogLabel{DisplayName: name, Kind: model.LabelKindGameBrand, LogoHash: logoHash}
	if err := testDB.Create(lbl).Error; err != nil {
		t.Fatalf("create label %s: %v", name, err)
	}
	return lbl.ID
}

// labelRelationInverse pairs each relation code with its mirror, so a fixture
// can assert a fact the way the importer stores it: BOTH directions.
var labelRelationInverse = map[int16]int16{
	model.LabelRelationParent:      model.LabelRelationSubsidiary,
	model.LabelRelationSubsidiary:  model.LabelRelationParent,
	model.LabelRelationImprint:     model.LabelRelationImprintOf,
	model.LabelRelationImprintOf:   model.LabelRelationImprint,
	model.LabelRelationSpawned:     model.LabelRelationOrigin,
	model.LabelRelationOrigin:      model.LabelRelationSpawned,
	model.LabelRelationSucceededBy: model.LabelRelationFormerly,
	model.LabelRelationFormerly:    model.LabelRelationSucceededBy,
}

// relateMirrored writes the fact "other is <relation> of label" the way the
// wave-186 importer does: the row and its inverse mirror.
func relateMirrored(t *testing.T, labelID, otherID int64, relation int16) {
	t.Helper()
	relateRaw(t, labelID, otherID, relation)
	relateRaw(t, otherID, labelID, labelRelationInverse[relation])
}

// relateRaw writes ONE stored row, unmirrored — for the cases that must pin
// what happens to a half-written or duplicated edge.
func relateRaw(t *testing.T, labelID, otherID int64, relation int16) {
	t.Helper()
	if err := testDB.Create(&model.CatalogLabelRelation{
		LabelID: labelID, OtherLabelID: otherID, Relation: relation,
		SourceID: srcVNDB, MatchedBy: "rule:test",
	}).Error; err != nil {
		t.Fatalf("relate %d→%d (%d): %v", labelID, otherID, relation, err)
	}
}

// graphNodeIDs is the node list as a set, for membership assertions.
func graphNodeIDs(t *testing.T, ids []int64) map[int64]bool {
	t.Helper()
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// TestLabelRelationGraphFamilyAndWorkCounts is the wave's core case: a
// three-level corporate family is reachable in ONE call from a leaf brand,
// every mirrored fact renders exactly once in the canonical direction, and each
// node's work_count is the browse lane's own nsfw-aware number.
func TestLabelRelationGraphFamilyAndWorkCounts(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// Grandparent → parent → two sibling brands, plus an imprint under one of
	// them: the shape a real publisher family has.
	grand := mkLabel(t, "AAA Holdings", "")
	parent := mkLabel(t, "BBB Corp", "logohash-bbb")
	seed := mkLabel(t, "CCC Brand", "logohash-ccc")
	sib := mkLabel(t, "DDD Brand", "")
	imprint := mkLabel(t, "EEE Imprint", "")

	relateMirrored(t, parent, grand, model.LabelRelationParent) // grand is parent of parent
	relateMirrored(t, seed, parent, model.LabelRelationParent)  // parent is parent of seed
	relateMirrored(t, sib, parent, model.LabelRelationParent)   // parent is parent of sib
	relateMirrored(t, sib, imprint, model.LabelRelationImprint) // imprint is imprint of sib

	// work_count material: one all-ages and one r18 work on the seed, both LIVE
	// claims (the only state the count counts since wave 146).
	wSafe := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "GraphSafe")
	wR18 := createWorkX(t, galgameMediumID, model.ContentRatingR18, model.WorkStatusLive, "GraphR18")
	for i, id := range []int64{wSafe.ID, wR18.ID} {
		claimLive(t, id, int64(9500+i))
	}
	for _, w := range []int64{wSafe.ID, wR18.ID} {
		if err := testDB.Create(&model.CatalogWorkLabel{
			WorkID: w, LabelID: seed, Kind: model.WorkLabelKindDeveloper,
		}).Error; err != nil {
			t.Fatalf("attach work label: %v", err)
		}
	}

	g, found, err := svc.LabelRelationGraph(ctx, seed, false)
	if err != nil || !found {
		t.Fatalf("graph: found=%v err=%v", found, err)
	}
	if len(g.Nodes) != 5 {
		t.Fatalf("nodes=%d want 5 (the whole family): %+v", len(g.Nodes), g.Nodes)
	}
	if g.Nodes[0].ID != seed {
		t.Fatalf("nodes[0]=%d want the seed %d", g.Nodes[0].ID, seed)
	}
	set := graphNodeIDs(t, []int64{grand, parent, seed, sib, imprint})
	for _, n := range g.Nodes {
		if !set[n.ID] {
			t.Fatalf("unexpected node %d", n.ID)
		}
	}
	// logo_hash rides the same projection as PublicLabel.LogoHash: the stored
	// hash verbatim, "" for a label with none — never omitted.
	for _, n := range g.Nodes {
		switch n.ID {
		case seed:
			if n.LogoHash != "logohash-ccc" {
				t.Fatalf("seed logo_hash=%q want logohash-ccc", n.LogoHash)
			}
			if n.WorkCount != 1 {
				t.Fatalf("sfw seed work_count=%d want 1 (r18 excluded)", n.WorkCount)
			}
		case parent:
			if n.LogoHash != "logohash-bbb" {
				t.Fatalf("parent logo_hash=%q", n.LogoHash)
			}
		default:
			if n.LogoHash != "" {
				t.Fatalf("node %d logo_hash=%q want \"\"", n.ID, n.LogoHash)
			}
		}
	}

	// Four facts were written, each MIRRORED — so eight stored rows and exactly
	// four edges, all in the canonical direction.
	if len(g.Edges) != 4 {
		t.Fatalf("edges=%d want 4 (mirrors folded): %+v", len(g.Edges), g.Edges)
	}
	want := map[[2]int64]string{
		{parent, grand}: "parent",
		{seed, parent}:  "parent",
		{sib, parent}:   "parent",
		{sib, imprint}:  "imprint",
	}
	for _, e := range g.Edges {
		rel, ok := want[[2]int64{e.From, e.To}]
		if !ok {
			t.Fatalf("unexpected edge %+v (an inverse leaked onto the face?)", e)
		}
		if e.Relation != rel {
			t.Fatalf("edge %+v relation want %s", e, rel)
		}
		delete(want, [2]int64{e.From, e.To})
	}
	if len(want) != 0 {
		t.Fatalf("missing edges: %+v", want)
	}

	// nsfw=true is the browse lane's other number, on every node.
	gn, _, err := svc.LabelRelationGraph(ctx, seed, true)
	if err != nil {
		t.Fatalf("nsfw graph: %v", err)
	}
	for _, n := range gn.Nodes {
		if n.ID == seed && n.WorkCount != 2 {
			t.Fatalf("nsfw seed work_count=%d want 2", n.WorkCount)
		}
	}

	// The one-hop face is untouched by this wave: it still renders BOTH sides
	// of the mirror for the seed (parent) and never learns the graph's bounds.
	rel, err := svc.labelRelations(ctx, parent)
	if err != nil {
		t.Fatalf("labelRelations: %v", err)
	}
	if len(rel) != 3 { // grand(parent) + seed(subsidiary) + sib(subsidiary)
		t.Fatalf("one-hop relations for parent = %d want 3: %+v", len(rel), rel)
	}
}

// TestLabelRelationGraphCycleAndDepth pins termination on a cyclic graph and
// the depth bound. A mirrored graph is cyclic by construction, and upstream
// also publishes genuine loops; without a visited set the walk never ends.
func TestLabelRelationGraphCycleAndDepth(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// A 3-cycle: a is parent of b, b is parent of c, c is parent of a.
	a := mkLabel(t, "Cycle A", "")
	b := mkLabel(t, "Cycle B", "")
	c := mkLabel(t, "Cycle C", "")
	relateMirrored(t, b, a, model.LabelRelationParent)
	relateMirrored(t, c, b, model.LabelRelationParent)
	relateMirrored(t, a, c, model.LabelRelationParent)

	g, found, err := svc.LabelRelationGraph(ctx, a, false)
	if err != nil || !found {
		t.Fatalf("cycle graph: found=%v err=%v", found, err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("cycle nodes=%d want 3 (each label once)", len(g.Nodes))
	}
	if len(g.Edges) != 3 {
		t.Fatalf("cycle edges=%d want 3: %+v", len(g.Edges), g.Edges)
	}

	// A straight chain LONGER than the depth bound: the walk stops at 4 hops,
	// so the 6th link is out of reach and its edge is not rendered either.
	cleanLabelGraphTables(t)
	chain := make([]int64, 7)
	for i := range chain {
		chain[i] = mkLabel(t, "Chain "+string(rune('A'+i)), "")
	}
	for i := 0; i+1 < len(chain); i++ {
		relateMirrored(t, chain[i], chain[i+1], model.LabelRelationParent)
	}
	g, _, err = svc.LabelRelationGraph(ctx, chain[0], false)
	if err != nil {
		t.Fatalf("chain graph: %v", err)
	}
	if len(g.Nodes) != labelGraphMaxDepth+1 {
		t.Fatalf("chain nodes=%d want %d (seed + %d hops)", len(g.Nodes), labelGraphMaxDepth+1, labelGraphMaxDepth)
	}
	if len(g.Edges) != labelGraphMaxDepth {
		t.Fatalf("chain edges=%d want %d", len(g.Edges), labelGraphMaxDepth)
	}
	for _, e := range g.Edges {
		if e.From == chain[5] || e.To == chain[5] || e.From == chain[6] || e.To == chain[6] {
			t.Fatalf("edge %+v reaches past the depth bound", e)
		}
	}
}

// TestLabelRelationGraphNodeCap pins the node cap and, more importantly, that
// it bites BREADTH-FIRST: what survives is the neighbourhood nearest the seed.
func TestLabelRelationGraphNodeCap(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	// A hub with 80 direct children — more than the cap on its own — and one
	// grandchild hanging off the LAST child, which the cap must therefore cut.
	hub := mkLabel(t, "Hub", "")
	children := make([]int64, 80)
	for i := range children {
		// Zero-padded so display_name order is the numeric order: the admission
		// order the cap applies is then predictable.
		children[i] = mkLabel(t, "Child "+pad2(i), "")
		relateMirrored(t, children[i], hub, model.LabelRelationParent)
	}
	deep := mkLabel(t, "Grandchild", "")
	relateMirrored(t, deep, children[79], model.LabelRelationParent)

	g, found, err := svc.LabelRelationGraph(ctx, hub, false)
	if err != nil || !found {
		t.Fatalf("cap graph: found=%v err=%v", found, err)
	}
	if len(g.Nodes) != labelGraphMaxNodes {
		t.Fatalf("nodes=%d want the cap %d", len(g.Nodes), labelGraphMaxNodes)
	}
	// Breadth-first: the survivors are the first depth-1 children, and the
	// depth-2 grandchild never gets in while depth-1 rows are still waiting.
	for _, n := range g.Nodes {
		if n.ID == deep {
			t.Fatalf("a depth-2 node displaced a depth-1 node")
		}
	}
	if g.Nodes[0].ID != hub {
		t.Fatalf("nodes[0]=%d want the seed", g.Nodes[0].ID)
	}
	// Every rendered edge stays INSIDE the node set — an edge to a label the
	// cap excluded would be a dangling reference a consumer cannot draw.
	inSet := map[int64]bool{}
	for _, n := range g.Nodes {
		inSet[n.ID] = true
	}
	for _, e := range g.Edges {
		if !inSet[e.From] || !inSet[e.To] {
			t.Fatalf("edge %+v dangles outside the node set", e)
		}
	}
	if len(g.Edges) != labelGraphMaxNodes-1 {
		t.Fatalf("edges=%d want %d (one per admitted child)", len(g.Edges), labelGraphMaxNodes-1)
	}
}

// pad2 renders 0-79 as a two-digit string so fixture display names sort
// numerically.
func pad2(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// TestLabelRelationGraphSoftDeletedExcluded pins that a merged-away label is
// invisible to the graph — the same red line labelRelations draws for the
// one-hop face, extended to every hop AND to the edges.
func TestLabelRelationGraphSoftDeletedExcluded(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	seed := mkLabel(t, "Live Seed", "")
	gone := mkLabel(t, "Merged Away", "")
	behind := mkLabel(t, "Behind The Dead", "")
	live := mkLabel(t, "Live Neighbour", "")
	relateMirrored(t, seed, gone, model.LabelRelationParent)
	relateMirrored(t, gone, behind, model.LabelRelationParent)
	relateMirrored(t, seed, live, model.LabelRelationImprint)

	if err := testDB.Exec(`UPDATE catalog_label SET deleted_at = now() WHERE id = ?`, gone).Error; err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	g, found, err := svc.LabelRelationGraph(ctx, seed, false)
	if err != nil || !found {
		t.Fatalf("graph: found=%v err=%v", found, err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes=%d want 2 (seed + the live neighbour): %+v", len(g.Nodes), g.Nodes)
	}
	for _, n := range g.Nodes {
		if n.ID == gone || n.ID == behind {
			t.Fatalf("node %d reached through a soft-deleted label", n.ID)
		}
	}
	if len(g.Edges) != 1 || g.Edges[0].To != live || g.Edges[0].Relation != "imprint" {
		t.Fatalf("edges=%+v want only the live imprint edge", g.Edges)
	}

	// The seed ITSELF being soft-deleted is a miss, not an empty graph.
	if err := testDB.Exec(`UPDATE catalog_label SET deleted_at = now() WHERE id = ?`, seed).Error; err != nil {
		t.Fatalf("soft-delete seed: %v", err)
	}
	if _, found, err = svc.LabelRelationGraph(ctx, seed, false); err != nil || found {
		t.Fatalf("soft-deleted seed: found=%v want false (err=%v)", found, err)
	}
}

// TestLabelRelationGraphSeedOnlyAndMiss pins the two degenerate answers: a
// label with no relations is a ONE-NODE graph (not a 404, and not a null
// edges list), and an id that does not exist is a miss.
func TestLabelRelationGraphSeedOnlyAndMiss(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	lonely := mkLabel(t, "Lonely Brand", "logohash-lonely")

	g, found, err := svc.LabelRelationGraph(ctx, lonely, false)
	if err != nil || !found {
		t.Fatalf("lonely graph: found=%v err=%v", found, err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].ID != lonely || g.Nodes[0].LogoHash != "logohash-lonely" {
		t.Fatalf("lonely nodes=%+v want just the seed", g.Nodes)
	}
	if g.Edges == nil || len(g.Edges) != 0 {
		t.Fatalf("lonely edges=%v want an empty, non-nil list", g.Edges)
	}

	if _, found, err = svc.LabelRelationGraph(ctx, lonely+99999, false); err != nil || found {
		t.Fatalf("unknown id: found=%v want false (err=%v)", found, err)
	}
}

// TestLabelRelationGraphMirrorDedup pins the mirror-folding rule on its own,
// including the two cases the family test cannot show: the canonical row stored
// under the FAR endpoint (so only its inverse is filed under the seed), and the
// same fact asserted by two sources.
func TestLabelRelationGraphMirrorDedup(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	seed := mkLabel(t, "Successor Brand", "")
	old := mkLabel(t, "Former Name", "")
	spun := mkLabel(t, "Spin Off", "")

	// "old is formerly of seed" / "seed is succeeded_by of old": the CANONICAL
	// code (succeeded_by) lives on the far endpoint's row.
	relateMirrored(t, seed, old, model.LabelRelationFormerly)
	// The same fact from a second source — one edge on the wire, not two.
	if err := testDB.Create(&model.CatalogLabelRelation{
		LabelID: old, OtherLabelID: seed, Relation: model.LabelRelationSucceededBy,
		SourceID: srcBangumiPub, MatchedBy: "rule:test-2",
	}).Error; err != nil {
		t.Fatalf("second source: %v", err)
	}
	relateMirrored(t, seed, spun, model.LabelRelationSpawned)

	g, found, err := svc.LabelRelationGraph(ctx, seed, false)
	if err != nil || !found {
		t.Fatalf("graph: found=%v err=%v", found, err)
	}
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes=%d want 3", len(g.Nodes))
	}
	if len(g.Edges) != 2 {
		t.Fatalf("edges=%d want 2 (mirror + multi-source folded): %+v", len(g.Edges), g.Edges)
	}
	got := map[[2]int64]string{}
	for _, e := range g.Edges {
		got[[2]int64{e.From, e.To}] = e.Relation
	}
	// succeeded_by is emitted from OLD (whose row holds the canonical code),
	// reading "seed is the succeeded_by of old" — the mirror under the seed
	// (formerly) never surfaces.
	if got[[2]int64{old, seed}] != "succeeded_by" {
		t.Fatalf("succession edge missing or inverted: %+v", g.Edges)
	}
	if got[[2]int64{seed, spun}] != "spawned" {
		t.Fatalf("spawn edge missing or inverted: %+v", g.Edges)
	}
}
