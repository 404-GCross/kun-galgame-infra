package editing_test

import (
	"errors"
	"testing"

	"api/internal/platform/editing"
)

// E3b owner-review: review evaluation = perm OR (overlay OwnerReview &&
// asserted IsEntityOwner). Three states pinned here: the owner adjudicates
// OwnerReview-overlaid fields, cannot touch fields without the capability,
// and other sites are entirely unaffected.

func ownerActor(uid int64, site string) editing.PolicyContext {
	pc := anonActor(uid)
	pc.Site = site
	pc.IsEntityOwner = true
	return pc
}

func TestOwnerReviewPolicy(t *testing.T) {
	perm := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever}
	withOwner := perm
	withOwner.OwnerReview = true

	owner := ownerActor(1, "anywhere")
	nonOwner := anonActor(2)

	if !withOwner.AllowsReview(owner) {
		t.Error("OwnerReview + IsEntityOwner must pass review")
	}
	if withOwner.AllowsReview(nonOwner) {
		t.Error("OwnerReview without ownership must not pass review")
	}
	if perm.AllowsReview(owner) {
		t.Error("ownership without OwnerReview must not pass review")
	}
	// The perm rule always also passes, ownership or not.
	if !withOwner.AllowsReview(reviewerActor(3)) {
		t.Error("review perm must still pass on an OwnerReview policy")
	}
}

func TestOwnerReviewEngine(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	onSite := func(pc editing.PolicyContext, site string) editing.PolicyContext {
		pc.Site = site
		return pc
	}
	propose := func(t *testing.T, site, key string, value any, uid int64) *editing.Proposal {
		t.Helper()
		prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: "test.widget", EntityID: 1,
			Patch: map[string]any{key: value}, Actor: onSite(anonActor(uid), site),
		})
		if err != nil {
			t.Fatalf("propose %s on %s: %v", key, site, err)
		}
		if rev != nil {
			t.Fatalf("proposal must stay open, got automerge")
		}
		return prop
	}
	owner := ownerActor(400, ownerReviewSite)

	// State 1: the owner (no perms) amends and merges an OwnerReview field.
	prop := propose(t, ownerReviewSite, fName, "proposed name", 300)
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set: map[string]any{fName: "owner-corrected name"}, Note: "fix", Actor: owner,
	}); err != nil {
		t.Fatalf("owner amend: %v", err)
	}
	rev, err := e.MergeProposal(testCtx, prop.ID, owner, "")
	if err != nil {
		t.Fatalf("owner merge: %v", err)
	}
	if rev.ActorUID != 300 || rev.AmenderUID == nil || *rev.AmenderUID != 400 {
		t.Fatalf("double signature: %+v", rev)
	}
	if w := loadWidget(t, 1); w.Name != "owner-corrected name" {
		t.Fatalf("apply: %+v", w)
	}

	// ... and declines one with a reason.
	prop = propose(t, ownerReviewSite, fName, "declined name", 300)
	if err := e.DeclineProposal(testCtx, prop.ID, owner, "not this"); err != nil {
		t.Fatalf("owner decline: %v", err)
	}

	// State 2: the same owner cannot adjudicate a field WITHOUT OwnerReview
	// (trusted_note keeps its field policy on the owner-review site).
	prop = propose(t, ownerReviewSite, fTrusted, "note", 301)
	var permErr *editing.PermissionError
	if _, err := e.MergeProposal(testCtx, prop.ID, owner, ""); !errors.As(err, &permErr) {
		t.Fatalf("owner on non-OwnerReview field must 403: %v", err)
	}

	// State 3: another site is unaffected — ownership grants nothing where
	// the overlay does not enable it (policies evaluate against the
	// PROPOSAL's site, so an asserted owner from anywhere is still denied).
	prop, _, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "default-site name"}, Actor: editorActor(302),
	})
	if err != nil {
		t.Fatalf("default-site proposal: %v", err)
	}
	if _, err := e.MergeProposal(testCtx, prop.ID, ownerActor(400, "test-site"), ""); !errors.As(err, &permErr) {
		t.Fatalf("owner on default site must 403: %v", err)
	}

	// The schema projection mirrors the evaluation: can_review only on the
	// OwnerReview field, and only for the asserted owner.
	proj, err := e.SchemaProjection(testCtx, "test.widget", 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range proj {
		want := p.Key == fName
		if p.CanReview != want {
			t.Errorf("owner projection %s: can_review=%v, want %v", p.Key, p.CanReview, want)
		}
	}
	proj, err = e.SchemaProjection(testCtx, "test.widget", 1, onSite(anonActor(5), ownerReviewSite))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range proj {
		if p.CanReview {
			t.Errorf("non-owner projection %s: can_review must be false", p.Key)
		}
	}
}
