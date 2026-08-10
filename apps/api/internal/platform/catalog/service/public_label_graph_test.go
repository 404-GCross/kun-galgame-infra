package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

func cleanLabelGraphTables(t *testing.T) {
	t.Helper()
	if err := testDB.Exec("TRUNCATE catalog_label_relation RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate catalog_label_relation: %v", err)
	}
}

func mkLabel(t *testing.T, name, logoHash string) int64 {
	t.Helper()
	lbl := &model.CatalogLabel{DisplayName: name, Kind: model.LabelKindGameBrand, LogoHash: logoHash}
	if err := testDB.Create(lbl).Error; err != nil {
		t.Fatalf("create label %s: %v", name, err)
	}
	return lbl.ID
}

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

func relateMirrored(t *testing.T, labelID, otherID int64, relation int16) {
	t.Helper()
	relateRaw(t, labelID, otherID, relation)
	relateRaw(t, otherID, labelID, labelRelationInverse[relation])
}

func relateRaw(t *testing.T, labelID, otherID int64, relation int16) {
	t.Helper()
	if err := testDB.Create(&model.CatalogLabelRelation{
		LabelID: labelID, OtherLabelID: otherID, Relation: relation,
		SourceID: srcVNDB, MatchedBy: "rule:test",
	}).Error; err != nil {
		t.Fatalf("relate %d→%d (%d): %v", labelID, otherID, relation, err)
	}
}

func graphNodeIDs(t *testing.T, ids []int64) map[int64]bool {
	t.Helper()
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func TestLabelRelationGraphFamilyAndWorkCounts(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	grand := mkLabel(t, "AAA Holdings", "")
	parent := mkLabel(t, "BBB Corp", "logohash-bbb")
	seed := mkLabel(t, "CCC Brand", "logohash-ccc")
	sib := mkLabel(t, "DDD Brand", "")
	imprint := mkLabel(t, "EEE Imprint", "")

	relateMirrored(t, parent, grand, model.LabelRelationParent)
	relateMirrored(t, seed, parent, model.LabelRelationParent)
	relateMirrored(t, sib, parent, model.LabelRelationParent)
	relateMirrored(t, sib, imprint, model.LabelRelationImprint)

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

	gn, _, err := svc.LabelRelationGraph(ctx, seed, true)
	if err != nil {
		t.Fatalf("nsfw graph: %v", err)
	}
	for _, n := range gn.Nodes {
		if n.ID == seed && n.WorkCount != 2 {
			t.Fatalf("nsfw seed work_count=%d want 2", n.WorkCount)
		}
	}

	rel, err := svc.labelRelations(ctx, parent)
	if err != nil {
		t.Fatalf("labelRelations: %v", err)
	}
	if len(rel) != 3 {
		t.Fatalf("one-hop relations for parent = %d want 3: %+v", len(rel), rel)
	}
}

func TestLabelRelationGraphCycleAndDepth(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

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

func TestLabelRelationGraphNodeCap(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	hub := mkLabel(t, "Hub", "")
	children := make([]int64, 80)
	for i := range children {
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
	for _, n := range g.Nodes {
		if n.ID == deep {
			t.Fatalf("a depth-2 node displaced a depth-1 node")
		}
	}
	if g.Nodes[0].ID != hub {
		t.Fatalf("nodes[0]=%d want the seed", g.Nodes[0].ID)
	}
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

func pad2(i int) string {
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

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

	if err := testDB.Exec(`UPDATE catalog_label SET deleted_at = now() WHERE id = ?`, seed).Error; err != nil {
		t.Fatalf("soft-delete seed: %v", err)
	}
	if _, found, err = svc.LabelRelationGraph(ctx, seed, false); err != nil || found {
		t.Fatalf("soft-deleted seed: found=%v want false (err=%v)", found, err)
	}
}

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

func TestLabelRelationGraphMirrorDedup(t *testing.T) {
	cleanTables(t)
	cleanLabelGraphTables(t)
	svc := newPublicSvc()
	ctx := t.Context()

	seed := mkLabel(t, "Successor Brand", "")
	old := mkLabel(t, "Former Name", "")
	spun := mkLabel(t, "Spin Off", "")

	relateMirrored(t, seed, old, model.LabelRelationFormerly)
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
	if got[[2]int64{old, seed}] != "succeeded_by" {
		t.Fatalf("succession edge missing or inverted: %+v", g.Edges)
	}
	if got[[2]int64{seed, spun}] != "spawned" {
		t.Fatalf("spawn edge missing or inverted: %+v", g.Edges)
	}
}
