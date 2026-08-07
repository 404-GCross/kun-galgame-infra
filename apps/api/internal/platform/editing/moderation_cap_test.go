package editing_test

import (
	"testing"

	"api/internal/platform/editing"
)

// The moderation cap (wave 187a): PolicyContext.ModerationCapped is the
// assembly point saying "this caller's surface is not a place where verdicts
// are reached". The engine owes two guarantees for it — no review standing, and
// no automerge — and owes them under EVERY rule, because the rules that land a
// write instantly are precisely the ones that never consult a permission key.

// cappedActor is the actor helpers' capped twin: same uid, same perms, same
// tier, one flag.
func cappedActor(pc editing.PolicyContext) editing.PolicyContext {
	pc.ModerationCapped = true
	return pc
}

// TestModerationCapDeniesReview is the cap as pure logic, beside the rules it
// caps: a full reviewer, and an asserted owner on an OwnerReview field, both
// lose review standing to the flag alone.
func TestModerationCapDeniesReview(t *testing.T) {
	perm := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever}
	reviewer := reviewerActor(1)
	if !perm.AllowsReview(reviewer) {
		t.Fatal("fixture: an uncapped reviewer must pass the review rule")
	}
	if perm.AllowsReview(cappedActor(reviewer)) {
		t.Error("a capped reviewer must not reach a verdict, whatever the permission says")
	}

	ownerReview := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview),
		Automerge: editing.AutomergeNever, OwnerReview: true}
	owner := anonActor(2)
	owner.IsEntityOwner = true
	if !ownerReview.AllowsReview(owner) {
		t.Fatal("fixture: an uncapped owner must pass an OwnerReview rule")
	}
	if ownerReview.AllowsReview(cappedActor(owner)) {
		t.Error("owning the entity is not standing to judge it from a capped surface")
	}

	// Proposing is untouched: the cap removes moderation, not participation.
	if !perm.AllowsPropose(cappedActor(anonActor(3))) {
		t.Error("a capped caller must still propose on an open field")
	}
}

// TestModerationCapDeniesAutomerge walks every automerge rule the registry
// accepts. "always" and "trusted" are the point: neither consults a permission
// key, so capping HasPerm alone would leave both landing a capped caller's
// write instantly. The "owner" rule keys on the entity's owner SITE and is
// evaluated inside the engine, so it is exercised through the engine below.
func TestModerationCapDeniesAutomerge(t *testing.T) {
	cases := []struct {
		rule  string
		actor editing.PolicyContext
	}{
		{editing.AutomergeAlways, anonActor(10)},
		{editing.AutomergeTrusted, trustedActor(11)},
		{editing.AutomergeReview, reviewerActor(12)},
	}
	for _, c := range cases {
		p := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: c.rule}
		if !p.AllowsAutomerge(c.actor) {
			t.Fatalf("fixture: automerge %q must pass uncapped", c.rule)
		}
		if p.AllowsAutomerge(cappedActor(c.actor)) {
			t.Errorf("automerge %q must not land a capped caller's write", c.rule)
		}
	}
}

// TestModerationCapDeniesOwnerAutomerge covers the one rule the pure-policy
// matrix cannot reach: automerge=owner, which the engine resolves against the
// spec's OwnerSite hook. A caller standing on the owning site direct-merges;
// the same caller from a capped surface files a proposal.
func TestModerationCapDeniesOwnerAutomerge(t *testing.T) {
	siteA := "site-a"
	cleanTables(t)
	createWidget(t, 1)
	reg := editing.NewRegistry()
	if err := reg.Register(ownedSpec(map[int64]*string{1: &siteA}, nil)); err != nil {
		t.Fatalf("register owned spec: %v", err)
	}
	e := editing.NewEngine(testDB, reg)

	onOwnerSite := anonActor(710)
	onOwnerSite.Site = siteA
	_, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.owned", EntityID: 1,
		Patch: map[string]any{"test.owned.name": "by the owner site"}, Actor: onOwnerSite,
	})
	if err != nil {
		t.Fatalf("fixture create: %v", err)
	}
	if rev == nil {
		t.Fatal("fixture: the owner site must automerge uncapped")
	}

	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.owned", EntityID: 1,
		Patch: map[string]any{"test.owned.name": "from a capped surface"}, Actor: cappedActor(onOwnerSite),
	})
	if err != nil {
		t.Fatalf("capped create: %v", err)
	}
	if rev != nil || prop.Status != editing.StatusOpen {
		t.Fatalf("automerge=owner must not land a capped caller's write: rev=%v status=%d", rev, prop.Status)
	}
}

// TestModerationCapOnAnOpenTenant is the cap where it actually bites, end to
// end through the engine: the overlay site opens `name` with automerge=always —
// the "open tenant" posture — and a capped caller's proposal waits there like
// anybody else's, with no revision behind it.
func TestModerationCapOnAnOpenTenant(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	open := reviewerActor(400)
	open.Site = overlaySite
	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "landed"}, Actor: open,
	})
	if err != nil {
		t.Fatalf("fixture create: %v", err)
	}
	if rev == nil || prop.Status != editing.StatusMerged {
		t.Fatal("fixture: the open tenant must automerge an uncapped caller")
	}

	prop, rev, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "queued"}, Actor: cappedActor(open),
	})
	if err != nil {
		t.Fatalf("capped create: %v", err)
	}
	if rev != nil || prop.Status != editing.StatusOpen {
		t.Fatalf("a capped caller must file an OPEN proposal on an open tenant: rev=%v status=%d", rev, prop.Status)
	}
	if w := loadWidget(t, 1); w.Name != "landed" {
		t.Fatalf("the capped patch must not have been applied: name=%q", w.Name)
	}

	// And it cannot then judge its own queue entry, nor anyone else's.
	if _, err := e.MergeProposal(testCtx, prop.ID, cappedActor(open), ""); err == nil {
		t.Error("a capped caller holding the review perm must not merge")
	}
	if err := e.DeclineProposal(testCtx, prop.ID, cappedActor(open), "no"); err == nil {
		t.Error("a capped caller holding the review perm must not decline")
	}
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set: map[string]any{fName: "amended"}, Actor: cappedActor(open),
	}); err == nil {
		t.Error("a capped caller holding the review perm must not amend")
	}
	// Withdrawing one's OWN proposal is not a verdict, and stays available.
	if err := e.WithdrawProposal(testCtx, prop.ID, cappedActor(open)); err != nil {
		t.Errorf("a capped proposer must still withdraw their own proposal: %v", err)
	}

	// The schema projection tells the same story, so a capped editor UI renders
	// the surface it actually has rather than buttons that 403.
	fields, err := e.SchemaProjection(testCtx, "test.widget", 1, cappedActor(open))
	if err != nil {
		t.Fatalf("projection: %v", err)
	}
	for _, f := range fields {
		if f.CanReview || f.WouldAutomerge {
			t.Errorf("capped projection %s: can_review=%v would_automerge=%v", f.Key, f.CanReview, f.WouldAutomerge)
		}
	}
}

// TestModerationCapUncappedIsUnchanged states the other half of "the cap can
// only ever remove": the zero value is the pre-187a engine, verbatim.
func TestModerationCapUncappedIsUnchanged(t *testing.T) {
	p := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeAlways}
	reviewer := reviewerActor(20)
	if reviewer.ModerationCapped {
		t.Fatal("the zero value must be uncapped")
	}
	if !p.AllowsReview(reviewer) || !p.AllowsAutomerge(reviewer) {
		t.Error("an uncapped reviewer keeps review standing and automerge")
	}
}
