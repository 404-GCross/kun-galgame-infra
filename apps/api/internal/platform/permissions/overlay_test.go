package permissions_test

import (
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/permissions"
)

const (
	demoAlpha  authz.Permission = "demo.alpha"
	demoBeta   authz.Permission = "demo.beta"
	foreignKey authz.Permission = "other.key"
)

func demoOwn() map[authz.Permission]bool {
	return map[authz.Permission]bool{demoAlpha: true, demoBeta: true}
}

// TestMergeWidensWithoutMutatingBase pins the additive contract: the overlay
// adds grants, and the base table it was built from is untouched (the base is
// a package-level var in every perm package — mutating it would corrupt the
// code floor for the rest of the process).
func TestMergeWidensWithoutMutatingBase(t *testing.T) {
	base := authz.Bundles{
		"moderator": {},
		"admin":     {demoAlpha},
		"ren":       {demoAlpha, demoBeta},
	}
	snap := permissions.Snapshot{"moderator": {demoAlpha}}

	merged := permissions.Merge(base, snap, demoOwn())

	if !authz.NewResolver(merged).Can([]string{"moderator"}, demoAlpha) {
		t.Error("overlay grant did not widen moderator")
	}
	if authz.NewResolver(base).Can([]string{"moderator"}, demoAlpha) {
		t.Error("Merge mutated the base bundles")
	}
	if len(base["moderator"]) != 0 {
		t.Errorf("base moderator bundle grew to %v", base["moderator"])
	}
}

// TestMergeIsAllowOnly pins that there is no way for the overlay to remove a
// code-floor grant — the whole point of the allow-only model.
func TestMergeIsAllowOnly(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha, demoBeta}}
	merged := permissions.Merge(base, permissions.Snapshot{}, demoOwn())
	r := authz.NewResolver(merged)
	if !r.Can([]string{"admin"}, demoAlpha) || !r.Can([]string{"admin"}, demoBeta) {
		t.Error("an empty overlay must leave the code floor intact")
	}
}

// TestMergeIgnoresForeignVocabulary pins that a domain's resolver never learns
// a key from a vocabulary it does not enforce.
func TestMergeIgnoresForeignVocabulary(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := permissions.Snapshot{"admin": {foreignKey}}

	merged := permissions.Merge(base, snap, demoOwn())

	if authz.NewResolver(merged).Can([]string{"admin"}, foreignKey) {
		t.Errorf("%q leaked into a domain that does not own it", foreignKey)
	}
}

// TestMergeIsIdempotent pins that re-applying a grant the code already makes
// does not duplicate it (a duplicate is harmless to Can but would grow the
// bundle on every refresh).
func TestMergeIsIdempotent(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := permissions.Snapshot{"admin": {demoAlpha}}

	merged := permissions.Merge(base, snap, demoOwn())

	if got := len(merged["admin"]); got != 1 {
		t.Errorf("admin bundle has %d entries, want 1: %v", got, merged["admin"])
	}
}

// TestHolderSwapTakesEffectThroughASharedReference pins the plumbing the whole
// invalidation design rests on: a gate that captured the Holder ONCE at startup
// must see a later swap. If enforcement points held the *Resolver instead, this
// test would fail and every refresh would be a no-op until restart.
func TestHolderSwapTakesEffectThroughASharedReference(t *testing.T) {
	base := authz.Bundles{"moderator": {}, "admin": {demoAlpha}}
	holder := authz.NewHolder(base)

	// What a route gate captures at registration time.
	var gate authz.Checker = holder

	if gate.Can([]string{"moderator"}, demoAlpha) {
		t.Fatal("moderator must start without the key")
	}

	holder.Swap(authz.NewResolver(
		permissions.Merge(base, permissions.Snapshot{"moderator": {demoAlpha}}, demoOwn()),
	))

	if !gate.Can([]string{"moderator"}, demoAlpha) {
		t.Error("the swap did not reach a gate holding the Holder")
	}

	// And a revoke (empty overlay) takes the role back to the code floor.
	holder.Swap(authz.NewResolver(permissions.Merge(base, permissions.Snapshot{}, demoOwn())))
	if gate.Can([]string{"moderator"}, demoAlpha) {
		t.Error("revoking did not return moderator to the code floor")
	}
	if !gate.Can([]string{"admin"}, demoAlpha) {
		t.Error("revoking a moderator grant must not touch admin's code floor")
	}
}
