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

// grantsOf / deniesOf build a Snapshot from the shorthand these tests read best
// in: role → keys.
func grantsOf(m map[string][]authz.Permission) permissions.Snapshot {
	snap := permissions.NewSnapshot()
	for role, perms := range m {
		for _, p := range perms {
			snap.Add(role, p, permissions.EffectGrant)
		}
	}
	return snap
}

func deniesOf(m map[string][]authz.Permission) permissions.Snapshot {
	snap := permissions.NewSnapshot()
	for role, perms := range m {
		for _, p := range perms {
			snap.Add(role, p, permissions.EffectDeny)
		}
	}
	return snap
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
	snap := grantsOf(map[string][]authz.Permission{"moderator": {demoAlpha}})

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

// TestMergeWithNoRowsIsTheCodeFloor pins that the code bundles are what runs
// when the table says nothing — including the case that matters most, a process
// that could not read the table at all.
func TestMergeWithNoRowsIsTheCodeFloor(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha, demoBeta}}
	merged := permissions.Merge(base, permissions.NewSnapshot(), demoOwn())
	r := authz.NewResolver(merged)
	if !r.Can([]string{"admin"}, demoAlpha) || !r.Can([]string{"admin"}, demoBeta) {
		t.Error("an empty overlay must leave the code floor intact")
	}
}

// TestMergeDeniesRemoveACodeFloorGrant pins the 2026-08-04 half of the model:
// an editable role can be narrowed below its bundle, and only for the key the
// row names.
func TestMergeDeniesRemoveACodeFloorGrant(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha, demoBeta}, "moderator": {demoAlpha}}
	snap := deniesOf(map[string][]authz.Permission{"admin": {demoAlpha}})

	r := authz.NewResolver(permissions.Merge(base, snap, demoOwn()))

	if r.Can([]string{"admin"}, demoAlpha) {
		t.Error("the deny did not remove admin's code-floor grant")
	}
	if !r.Can([]string{"admin"}, demoBeta) {
		t.Error("the deny reached a key it does not name")
	}
	if !r.Can([]string{"moderator"}, demoAlpha) {
		t.Error("the deny reached a role it does not name")
	}
	if len(base["admin"]) != 2 {
		t.Errorf("Merge mutated the base bundle: %v", base["admin"])
	}
}

// TestMergeNeverDeniesRen is the fuse. ren is how an operator who has locked
// everyone else out gets back in, so a deny row naming it — however it got
// there, including a hand-written INSERT that never passed the validator — must
// be inert at the one place the table becomes enforcement.
func TestMergeNeverDeniesRen(t *testing.T) {
	base := authz.Bundles{"ren": {demoAlpha, demoBeta}, "admin": {demoAlpha}}
	snap := deniesOf(map[string][]authz.Permission{
		"ren":   {demoAlpha, demoBeta},
		"admin": {demoAlpha},
	})

	r := authz.NewResolver(permissions.Merge(base, snap, demoOwn()))

	if !r.Can([]string{"ren"}, demoAlpha) || !r.Can([]string{"ren"}, demoBeta) {
		t.Error("a deny row narrowed ren — the lockout fuse is gone")
	}
	if r.Can([]string{"admin"}, demoAlpha) {
		t.Error("ren's immunity leaked to admin")
	}
}

// TestMergeIgnoresForeignDenies pins that the own-vocabulary restriction cuts
// both ways: a domain must not lose a key because another domain's vocabulary
// happens to spell one the same way.
func TestMergeIgnoresForeignDenies(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	// foreignKey is not in demoOwn(), and neither is it in this bundle — the
	// case that would bite is a deny arriving for a key this domain DOES grant
	// but does not own, so assert on the one it does own staying put.
	snap := deniesOf(map[string][]authz.Permission{"admin": {foreignKey}})

	r := authz.NewResolver(permissions.Merge(base, snap, demoOwn()))

	if !r.Can([]string{"admin"}, demoAlpha) {
		t.Error("a foreign-vocabulary deny disturbed this domain's table")
	}
}

// TestMergeDenyBeatsAGrantForTheSamePair pins the resolution order for a state
// the unique index makes unreachable through the API but a dump restore could
// still produce: the narrower answer wins.
func TestMergeDenyBeatsAGrantForTheSamePair(t *testing.T) {
	base := authz.Bundles{"admin": {}}
	snap := permissions.NewSnapshot()
	snap.Add("admin", demoAlpha, permissions.EffectGrant)
	snap.Add("admin", demoAlpha, permissions.EffectDeny)

	if authz.NewResolver(permissions.Merge(base, snap, demoOwn())).Can([]string{"admin"}, demoAlpha) {
		t.Error("a contradictory pair resolved to the widening answer")
	}
}

// TestMergeIgnoresAnUnknownEffect pins that a row whose effect is neither grant
// nor deny changes nothing in EITHER direction.
func TestMergeIgnoresAnUnknownEffect(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := permissions.NewSnapshot()
	snap.Add("admin", demoAlpha, "")
	snap.Add("admin", demoBeta, "revoke")

	r := authz.NewResolver(permissions.Merge(base, snap, demoOwn()))

	if !r.Can([]string{"admin"}, demoAlpha) {
		t.Error("an unrecognized effect removed a code-floor grant")
	}
	if r.Can([]string{"admin"}, demoBeta) {
		t.Error("an unrecognized effect added a grant")
	}
}

// TestMergeIgnoresForeignVocabulary pins that a domain's resolver never learns
// a key from a vocabulary it does not enforce.
func TestMergeIgnoresForeignVocabulary(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := grantsOf(map[string][]authz.Permission{"admin": {foreignKey}})

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
	snap := grantsOf(map[string][]authz.Permission{"admin": {demoAlpha}})

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
		permissions.Merge(base, grantsOf(map[string][]authz.Permission{"moderator": {demoAlpha}}), demoOwn()),
	))

	if !gate.Can([]string{"moderator"}, demoAlpha) {
		t.Error("the swap did not reach a gate holding the Holder")
	}

	// And a revoke (empty overlay) takes the role back to the code floor.
	holder.Swap(authz.NewResolver(permissions.Merge(base, permissions.NewSnapshot(), demoOwn())))
	if gate.Can([]string{"moderator"}, demoAlpha) {
		t.Error("revoking did not return moderator to the code floor")
	}
	if !gate.Can([]string{"admin"}, demoAlpha) {
		t.Error("revoking a moderator grant must not touch admin's code floor")
	}
}
