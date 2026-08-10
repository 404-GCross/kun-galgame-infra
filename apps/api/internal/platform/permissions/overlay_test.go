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

func TestMergeWithNoRowsIsTheCodeFloor(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha, demoBeta}}
	merged := permissions.Merge(base, permissions.NewSnapshot(), demoOwn())
	r := authz.NewResolver(merged)
	if !r.Can([]string{"admin"}, demoAlpha) || !r.Can([]string{"admin"}, demoBeta) {
		t.Error("an empty overlay must leave the code floor intact")
	}
}

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

func TestMergeIgnoresForeignDenies(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := deniesOf(map[string][]authz.Permission{"admin": {foreignKey}})

	r := authz.NewResolver(permissions.Merge(base, snap, demoOwn()))

	if !r.Can([]string{"admin"}, demoAlpha) {
		t.Error("a foreign-vocabulary deny disturbed this domain's table")
	}
}

func TestMergeDenyBeatsAGrantForTheSamePair(t *testing.T) {
	base := authz.Bundles{"admin": {}}
	snap := permissions.NewSnapshot()
	snap.Add("admin", demoAlpha, permissions.EffectGrant)
	snap.Add("admin", demoAlpha, permissions.EffectDeny)

	if authz.NewResolver(permissions.Merge(base, snap, demoOwn())).Can([]string{"admin"}, demoAlpha) {
		t.Error("a contradictory pair resolved to the widening answer")
	}
}

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

func TestMergeIgnoresForeignVocabulary(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := grantsOf(map[string][]authz.Permission{"admin": {foreignKey}})

	merged := permissions.Merge(base, snap, demoOwn())

	if authz.NewResolver(merged).Can([]string{"admin"}, foreignKey) {
		t.Errorf("%q leaked into a domain that does not own it", foreignKey)
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	base := authz.Bundles{"admin": {demoAlpha}}
	snap := grantsOf(map[string][]authz.Permission{"admin": {demoAlpha}})

	merged := permissions.Merge(base, snap, demoOwn())

	if got := len(merged["admin"]); got != 1 {
		t.Errorf("admin bundle has %d entries, want 1: %v", got, merged["admin"])
	}
}

func TestHolderSwapTakesEffectThroughASharedReference(t *testing.T) {
	base := authz.Bundles{"moderator": {}, "admin": {demoAlpha}}
	holder := authz.NewHolder(base)

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

	holder.Swap(authz.NewResolver(permissions.Merge(base, permissions.NewSnapshot(), demoOwn())))
	if gate.Can([]string{"moderator"}, demoAlpha) {
		t.Error("revoking did not return moderator to the code floor")
	}
	if !gate.Can([]string{"admin"}, demoAlpha) {
		t.Error("revoking a moderator grant must not touch admin's code floor")
	}
}
