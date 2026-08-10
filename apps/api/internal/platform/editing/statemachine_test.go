package editing_test

import (
	"errors"
	"testing"

	"api/internal/platform/editing"
)

func TestProposeThenMerge(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "Widget A", fOLang: "en"},
		Note:  "fix name", Actor: editorActor(100),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rev != nil {
		t.Fatal("automerge=never must not merge on create")
	}
	if prop.Status != editing.StatusOpen || prop.BaseRevisionSeq != 0 {
		t.Fatalf("fresh proposal: status=%d base=%d", prop.Status, prop.BaseRevisionSeq)
	}

	rev, err = e.MergeProposal(testCtx, prop.ID, reviewerActor(200), "lgtm")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if rev.Seq != 1 || rev.Action != editing.ActionMerged {
		t.Fatalf("revision: seq=%d action=%d", rev.Seq, rev.Action)
	}
	if rev.ActorUID != 100 {
		t.Fatalf("revision actor must be the proposer, got %d", rev.ActorUID)
	}
	if rev.AmenderUID != nil {
		t.Fatal("no amendments → amender must be nil")
	}

	w := loadWidget(t, 1)
	if w.Name != "Widget A" || w.OLang != "en" {
		t.Fatalf("apply missed: %+v", w)
	}

	got, _, eff, err := e.GetProposal(testCtx, prop.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != editing.StatusMerged {
		t.Fatalf("status=%d, want merged", got.Status)
	}
	if got.DecidedByUID == nil || *got.DecidedByUID != 200 || got.DecisionNote != "lgtm" {
		t.Fatalf("decision bookkeeping: %+v", got)
	}
	if len(eff) != 2 {
		t.Fatalf("effective patch: %v", eff)
	}

	if _, err := e.MergeProposal(testCtx, prop.ID, reviewerActor(200), ""); !errors.Is(err, editing.ErrNotOpen) {
		t.Fatalf("re-merge: %v", err)
	}
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set: map[string]any{fName: "x"}, Actor: reviewerActor(200),
	}); !errors.Is(err, editing.ErrNotOpen) {
		t.Fatalf("amend merged: %v", err)
	}
	if err := e.DeclineProposal(testCtx, prop.ID, reviewerActor(200), "no"); !errors.Is(err, editing.ErrNotOpen) {
		t.Fatalf("decline merged: %v", err)
	}
	if err := e.WithdrawProposal(testCtx, prop.ID, editorActor(100)); !errors.Is(err, editing.ErrNotOpen) {
		t.Fatalf("withdraw merged: %v", err)
	}
}

func TestDecline(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)
	prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "nope"}, Actor: editorActor(100),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := e.DeclineProposal(testCtx, prop.ID, editorActor(100), "self"); err == nil {
		t.Fatal("decline without review perm must fail")
	}

	if err := e.DeclineProposal(testCtx, prop.ID, reviewerActor(200), "wrong name"); err != nil {
		t.Fatalf("decline: %v", err)
	}
	got, _, _, err := e.GetProposal(testCtx, prop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != editing.StatusDeclined || got.DecisionNote != "wrong name" {
		t.Fatalf("declined bookkeeping: %+v", got)
	}
	if w := loadWidget(t, 1); w.Name != "" {
		t.Fatal("decline must not touch the entity")
	}

	var revCount int64
	testDB.Model(&editing.Revision{}).Count(&revCount)
	if revCount != 0 {
		t.Fatal("decline must not write a revision")
	}
}

func TestWithdraw(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)
	prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "mine"}, Actor: editorActor(100),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := e.WithdrawProposal(testCtx, prop.ID, editorActor(999)); !errors.Is(err, editing.ErrNotProposer) {
		t.Fatalf("withdraw by non-proposer: %v", err)
	}
	if err := e.WithdrawProposal(testCtx, prop.ID, editorActor(100)); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	got, _, _, _ := e.GetProposal(testCtx, prop.ID)
	if got.Status != editing.StatusWithdrawn {
		t.Fatalf("status=%d, want withdrawn", got.Status)
	}
}

func TestDirectEditSugar(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fOpen: "hello"}, Actor: anonActor(300),
	})
	if err != nil {
		t.Fatalf("direct edit: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect || rev.Seq != 1 {
		t.Fatalf("direct revision: %+v", rev)
	}
	got, _, _, _ := e.GetProposal(testCtx, prop.ID)
	if got.Status != editing.StatusMerged {
		t.Fatalf("sugar proposal status=%d, want merged", got.Status)
	}
	if w := loadWidget(t, 1); w.OpenNote != "hello" {
		t.Fatal("direct edit did not apply")
	}
	var revCount int64
	testDB.Model(&editing.Revision{}).Count(&revCount)
	if revCount != 1 {
		t.Fatalf("exactly one revision, got %d", revCount)
	}
}

func TestAutomergeTrusted(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	_, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fTrusted: "by trusted"}, Actor: trustedActor(300),
	})
	if err != nil {
		t.Fatalf("trusted create: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatal("trusted proposer must automerge on an automerge=trusted field")
	}

	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fTrusted: "by anon"}, Actor: anonActor(301),
	})
	if err != nil {
		t.Fatalf("anon create: %v", err)
	}
	if rev != nil {
		t.Fatal("TL0 proposer must not automerge")
	}
	if prop.Status != editing.StatusOpen {
		t.Fatalf("status=%d, want open", prop.Status)
	}

	prop, rev, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fOpen: "x", fTrusted: "y"}, Actor: anonActor(302),
	})
	if err != nil {
		t.Fatalf("mixed create: %v", err)
	}
	if rev != nil || prop.Status != editing.StatusOpen {
		t.Fatal("mixed patch must not automerge when one field's rule fails")
	}
}
