package editbridge

import (
	"encoding/json"
	"testing"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
)

// dumpEditTables snapshots both engine tables as normalized JSON for
// byte-level idempotency comparison.
func dumpEditTables(t *testing.T) string {
	t.Helper()
	var revs []revisionRow
	if err := testDB.Order("id ASC").Find(&revs).Error; err != nil {
		t.Fatal(err)
	}
	var props []proposalRow
	if err := testDB.Order("id ASC").Find(&props).Error; err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"revisions": revs, "proposals": props})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTransformIdempotencyAndDelta(t *testing.T) {
	seedFixture(t)
	first := runTransform(t)
	if first.RevisionsProcessed != 6 || first.RevisionsMigrated != 6 ||
		first.ProposalsProcessed != 1 || first.ProposalsMigrated != 1 ||
		first.DroppedPending != 1 || first.DroppedDeclined != 1 {
		t.Fatalf("first pass summary: %+v", first)
	}
	dump1 := dumpEditTables(t)

	// Idempotency: a second full pass is a byte-level no-op.
	second := runTransform(t)
	if second.RevisionsMigrated != 0 || second.ProposalsMigrated != 0 {
		t.Fatalf("second pass migrated rows: %+v", second)
	}
	if dump2 := dumpEditTables(t); dump2 != dump1 {
		t.Fatalf("second pass changed table content")
	}
	if _, err := Verify(testDB, testDB, 0); err != nil {
		t.Fatalf("verify after re-run: %v", err)
	}

	// Delta: one more source row (the freeze-window catch-up) migrates alone.
	s6 := snapA("新名二", "简介二", []int{1, 2})
	r6 := &model.GalgameRevision{
		ID: revIDBase + 6, GalgameID: gidA, Revision: 6, UserID: 9,
		Action: "updated", Snapshot: mustJSON(t, s6),
	}
	r6.SetChangedFields([]string{"name_zh_cn"})
	insertRevision(t, r6)

	third := runTransform(t)
	if third.RevisionsMigrated != 1 {
		t.Fatalf("delta pass migrated %d revisions, want 1", third.RevisionsMigrated)
	}
	wire, err := testBridge.GetRevision(t.Context(), gidA, 6)
	if err != nil || wire.ID != revIDBase+6 || wire.Action != "updated" {
		t.Fatalf("delta row wire = %+v, %v", wire, err)
	}
	if _, err := Verify(testDB, testDB, 0); err != nil {
		t.Fatalf("verify after delta: %v", err)
	}
}

func TestSequenceBumpKeepsNewIDsAboveLegacy(t *testing.T) {
	seedFixture(t)
	runTransform(t)

	// The fixture's legacy ids (1001+) sit far above the engine identity
	// counter (single digits before the bump) — the production posture. A
	// post-transform engine write must mint an id ABOVE every legacy wire id
	// or downstream since_id cursors would silently skip it.
	rev := &editing.Revision{
		EntityFamily: "galgame", EntityType: editspec.TypeGame, EntityID: gidA,
		Seq: 7, Action: editing.ActionDirect,
		ChangedFields: []byte(`["galgame.game.status"]`), Snapshot: []byte(`{}`),
		ActorUID: 1, Site: editspec.SiteGalgameWiki,
	}
	if err := testDB.Create(rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.ID <= revIDBase+11 {
		t.Fatalf("new engine revision id %d not above max legacy id %d", rev.ID, revIDBase+11)
	}

	var prop editing.Proposal
	prop = editing.Proposal{
		EntityFamily: "galgame", EntityType: editspec.TypeGame, EntityID: gidA,
		BaseRevisionSeq: 6, Patch: []byte(`{}`), ProposerUID: 1,
		Site: editspec.SiteGalgameWiki, Status: editing.StatusOpen,
	}
	if err := testDB.Create(&prop).Error; err != nil {
		t.Fatal(err)
	}
	if prop.ID <= prIDBase+3 {
		t.Fatalf("new engine proposal id %d not above max legacy pr id %d", prop.ID, prIDBase+3)
	}
}
