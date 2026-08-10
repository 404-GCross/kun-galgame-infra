package editing_test

import (
	"fmt"
	"testing"

	"api/internal/platform/editing"
)

func TestListProposalsTotalIgnoresLimit(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	const mine, other = int64(100), int64(101)
	for i := 0; i < 7; i++ {
		uid := mine
		if i%2 == 1 {
			uid = other
		}
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: "test.widget", EntityID: 1,
			Patch: map[string]any{fName: fmt.Sprintf("Widget %d", i)},
			Actor: editorActor(uid),
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	items, total, err := e.ListProposalsWithTotal(testCtx, editing.ProposalFilter{
		EntityType: "test.widget", Status: -1, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("page: %d rows", len(items))
	}
	if total != 7 {
		t.Fatalf("total = %d, want 7 (the whole filtered set, not the page)", total)
	}

	_, total, err = e.ListProposalsWithTotal(testCtx, editing.ProposalFilter{
		EntityType: "test.widget", ProposerUID: mine, Status: -1, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Fatalf("per-proposer total = %d, want 4", total)
	}
	_, total, err = e.ListProposalsWithTotal(testCtx, editing.ProposalFilter{
		EntityType: "test.widget", Status: editing.StatusMerged,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("merged total = %d, want 0 (nothing merged yet)", total)
	}

	page, err := e.ListProposals(testCtx, editing.ProposalFilter{
		EntityType: "test.widget", Status: -1, Limit: 2,
	})
	if err != nil || len(page) != 2 {
		t.Fatalf("legacy list: %d rows err=%v", len(page), err)
	}
}
