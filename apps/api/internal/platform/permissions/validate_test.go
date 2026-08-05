package permissions_test

import (
	"strings"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/permissions"
)

// A synthetic domain, so the validator's rules are tested against a table this
// file controls rather than against the live console vocabulary (which would
// make every future key change break these tests for no reason).
//
//	ren   : locked, alpha, beta, delta
//	admin : alpha, delta
//	mod   : delta
//
// gamma is deliberately in NO bundle: it is the key that proves the admin ⊆ ren
// half of containment can actually fail. delta is held all the way down the
// axis, which is what makes it the key the deny rules can be exercised on — a
// deny needs something to take away.
const (
	tAlpha  authz.Permission = "demo.alpha"
	tBeta   authz.Permission = "demo.beta"
	tGamma  authz.Permission = "demo.gamma"
	tDelta  authz.Permission = "demo.delta"
	tLocked authz.Permission = "demo.locked"
)

// testRig is a registry over the synthetic domain plus the live holder it
// swaps to simulate applied overlay state.
type testRig struct {
	reg    *permissions.Registry
	holder *authz.Holder
	base   authz.Bundles
	own    map[authz.Permission]bool
	val    *permissions.Validator
}

func newRig() *testRig {
	base := authz.Bundles{
		"moderator": {tDelta},
		"admin":     {tAlpha, tDelta},
		"ren":       {tAlpha, tBeta, tLocked, tDelta},
	}
	holder := authz.NewHolder(base)
	reg := permissions.NewRegistry(permissions.Domain{
		Name:         "demo",
		TitleZH:      "演示",
		Bundles:      base,
		Holder:       holder,
		NonDelegable: authz.NonDelegable{tLocked: true},
		Keys: []permissions.Key{
			{Permission: tAlpha, DescEN: "alpha", DescZH: "alpha"},
			{Permission: tBeta, DescEN: "beta", DescZH: "beta"},
			{Permission: tGamma, DescEN: "gamma", DescZH: "gamma"},
			{Permission: tDelta, DescEN: "delta", DescZH: "delta"},
			{Permission: tLocked, DescEN: "locked", DescZH: "locked"},
		},
	})
	return &testRig{
		reg: reg, holder: holder, base: base,
		own: map[authz.Permission]bool{
			tAlpha: true, tBeta: true, tGamma: true, tDelta: true, tLocked: true,
		},
		val: permissions.NewValidator(reg),
	}
}

// rows is the shorthand these tests describe an overlay with: role → keys, one
// map per effect.
type rows map[string][]authz.Permission

// apply installs an overlay state as if it had been written — merged into the
// live holder AND handed to the validator — so every rule is exercised against
// a starting point that is internally consistent.
func (r *testRig) apply(grants, denies rows) permissions.OverlayState {
	snap := permissions.NewSnapshot()
	st := permissions.OverlayState{}
	for effect, m := range map[string]rows{
		permissions.EffectGrant: grants,
		permissions.EffectDeny:  denies,
	} {
		for role, perms := range m {
			for _, p := range perms {
				snap.Add(role, p, effect)
				st.Set(role, p, effect)
			}
		}
	}
	r.holder.Swap(authz.NewResolver(permissions.Merge(r.base, snap, r.own)))
	return st
}

// clean is the common starting point: no overlay rows at all.
func (r *testRig) clean() permissions.OverlayState { return r.apply(nil, nil) }

func act(op permissions.Op, role string, p authz.Permission) permissions.Action {
	return permissions.Action{Op: op, Role: role, Permission: p}
}

func grant(role string, p authz.Permission) permissions.Action {
	return act(permissions.OpGrant, role, p)
}

func revoke(role string, p authz.Permission) permissions.Action {
	return act(permissions.OpRevokeGrant, role, p)
}

func deny(role string, p authz.Permission) permissions.Action {
	return act(permissions.OpDeny, role, p)
}

func restore(role string, p authz.Permission) permissions.Action {
	return act(permissions.OpRevokeDeny, role, p)
}

var (
	renCaller = permissions.Caller{UserID: 1, Roles: []string{"admin", "ren"}}
	adminUser = permissions.Caller{UserID: 2, Roles: []string{"admin"}}
	modCaller = permissions.Caller{UserID: 3, Roles: []string{"moderator"}}
)

// --- Rule 1: known key, editable role -------------------------------------

func TestRule1RejectsImmutableAndUnknownRoles(t *testing.T) {
	rig := newRig()
	st := rig.clean()

	cases := []struct {
		name string
		role string
		want string
	}{
		{"user rows are immutable", "user", "roles 为空数组"},
		{"ren rows are immutable", "ren", "上界"},
		{"unknown role", "ghost", "未知角色"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := rig.val.Validate(renCaller, st, grant(c.role, tBeta))
			assertRejected(t, err, c.want)
		})
	}
}

func TestRule1RejectsUnknownPermission(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	err := rig.val.Validate(renCaller, st, grant("moderator", authz.Permission("demo.nonexistent")))
	assertRejected(t, err, "未知权限键")
}

// --- Rule 3: non-delegable keys -------------------------------------------

func TestRule3NonDelegableIsRefusedEvenForRen(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// ren holds oauth.permissions.manage, so this caller bypasses delegation —
	// and is still refused.
	err := rig.val.Validate(renCaller, st, grant("admin", tLocked))
	assertRejected(t, err, "不可委派")
}

// --- Rule 2: delegation ----------------------------------------------------

func TestRule2TargetMustBeStrictlyBelowCaller(t *testing.T) {
	rig := newRig()
	st := rig.clean()

	// admin → admin: same tier, refused.
	assertRejected(t, rig.val.Validate(adminUser, st, grant("admin", tBeta)), "严格低于")
	// moderator → moderator: same tier, refused.
	assertRejected(t, rig.val.Validate(modCaller, st, grant("moderator", tAlpha)), "严格低于")
}

func TestRule2CreatorRowIsRenOnly(t *testing.T) {
	rig := newRig()
	st := rig.clean()

	assertRejected(t, rig.val.Validate(adminUser, st, grant("creator", tAlpha)), "仅 ren 可编辑")

	// ren may edit it — creator is off the management axis, so containment
	// does not constrain the grant.
	if err := rig.val.Validate(renCaller, st, grant("creator", tAlpha)); err != nil {
		t.Errorf("ren must be able to grant on the creator row: %v", err)
	}
}

func TestRule2CallerMustHoldThePermission(t *testing.T) {
	rig := newRig()
	st := rig.clean()

	// admin holds alpha but not beta.
	assertRejected(t, rig.val.Validate(adminUser, st, grant("moderator", tBeta)), "自己都不持有")

	if err := rig.val.Validate(adminUser, st, grant("moderator", tAlpha)); err != nil {
		t.Errorf("admin delegating a key it holds, downward, must pass: %v", err)
	}
}

func TestRule2IsLiftedByPermissionsManage(t *testing.T) {
	rig := newRig()
	st := rig.apply(rows{"admin": {tBeta}}, nil)

	// ren holds oauth.permissions.manage: it may grant a key to any editable
	// role without the rank/self-holding checks. (admin already has beta via
	// the overlay, so containment holds.)
	if err := rig.val.Validate(renCaller, st, grant("moderator", tBeta)); err != nil {
		t.Errorf("permissions.manage must lift the delegation rule: %v", err)
	}
}

// --- State preconditions ---------------------------------------------------

func TestGrantingAnAlreadyEffectiveKeyIsRefused(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	assertRejected(t, rig.val.Validate(renCaller, st, grant("admin", tAlpha)), "无需重复授予")
}

func TestRevokingACodeFloorGrantPointsAtTheDenyOperation(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// admin holds alpha from the CODE bundle, not the overlay: there is no row
	// to delete. The key IS reachable now — but through the other door, and the
	// rejection has to say which one.
	err := rig.val.Validate(renCaller, st, revoke("admin", tAlpha))
	assertRejected(t, err, "来自代码捆")
	assertRejected(t, err, "撤销(记 deny)")
}

func TestRevokingAnOverlayGrantIsAllowed(t *testing.T) {
	rig := newRig()
	st := rig.apply(rows{"admin": {tBeta}}, nil)
	if err := rig.val.Validate(renCaller, st, revoke("admin", tBeta)); err != nil {
		t.Errorf("revoking an overlay grant must pass: %v", err)
	}
}

// --- Rule 4: containment ---------------------------------------------------

func TestRule4GrantRejectedWhenAdminLacksTheKey(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// moderator ⊆ admin would break: admin does not hold beta.
	err := rig.val.Validate(renCaller, st, grant("moderator", tBeta))
	assertRejected(t, err, "请先把")
	assertRejected(t, err, "授予 admin")
}

func TestRule4GrantRejectedWhenRenLacksTheKey(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// admin ⊆ ren would break: gamma is in no bundle at all, so ren lacks it
	// and no overlay row can put it there (ren rows are immutable).
	assertRejected(t, rig.val.Validate(renCaller, st, grant("admin", tGamma)), "ren 的代码捆不含")
}

func TestRule4RevokeRejectedWhenModeratorStillHoldsTheKey(t *testing.T) {
	rig := newRig()
	st := rig.apply(rows{"admin": {tBeta}, "moderator": {tBeta}}, nil)
	// Cutting admin while moderator keeps it inverts the axis.
	err := rig.val.Validate(renCaller, st, revoke("admin", tBeta))
	assertRejected(t, err, "请先撤销 moderator")
}

func TestRule4AllowsTheOrderedSequence(t *testing.T) {
	rig := newRig()

	// 1. Grant admin first — allowed, ren holds beta.
	st := rig.clean()
	if err := rig.val.Validate(renCaller, st, grant("admin", tBeta)); err != nil {
		t.Fatalf("granting admin first must pass: %v", err)
	}

	// 2. Then moderator — now allowed.
	st = rig.apply(rows{"admin": {tBeta}}, nil)
	if err := rig.val.Validate(renCaller, st, grant("moderator", tBeta)); err != nil {
		t.Fatalf("granting moderator after admin must pass: %v", err)
	}

	// 3. Unwinding works in the reverse order: moderator first.
	st = rig.apply(rows{"admin": {tBeta}, "moderator": {tBeta}}, nil)
	if err := rig.val.Validate(renCaller, st, revoke("moderator", tBeta)); err != nil {
		t.Fatalf("revoking moderator first must pass: %v", err)
	}
	st = rig.apply(rows{"admin": {tBeta}}, nil)
	if err := rig.val.Validate(renCaller, st, revoke("admin", tBeta)); err != nil {
		t.Fatalf("revoking admin after moderator must pass: %v", err)
	}
}

// --- Deny: the row must be one a deny can even reach -----------------------

func TestDenyIsRefusedOnTheImmutableRows(t *testing.T) {
	rig := newRig()
	st := rig.clean()

	// ren is the fuse: no DB state may narrow it, and the validator says so
	// before Merge ever has to be the one enforcing it.
	assertRejected(t, rig.val.Validate(renCaller, st, deny("ren", tAlpha)), "ren 行不可编辑")
	assertRejected(t, rig.val.Validate(renCaller, st, deny("user", tAlpha)), "user 行不可编辑")
}

func TestDenyRequiresALiveKey(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	err := rig.val.Validate(renCaller, st, deny("admin", authz.Permission("demo.nonexistent")))
	assertRejected(t, err, "未知权限键")
}

// --- Deny: the rank rule is the grant rank rule ----------------------------

func TestDenyTargetMustBeStrictlyBelowCaller(t *testing.T) {
	rig := newRig()
	st := rig.clean()

	// An admin cannot deny the admin row — which is the same statement as "no
	// self-deny", since an admin caller IS the admin row. Only ren, whose
	// permissions.manage lifts the rule, can reach it.
	assertRejected(t, rig.val.Validate(adminUser, st, deny("admin", tAlpha)), "严格低于")
	assertRejected(t, rig.val.Validate(modCaller, st, deny("moderator", tDelta)), "严格低于")

	if err := rig.val.Validate(renCaller, st, deny("admin", tAlpha)); err != nil {
		t.Errorf("ren must be able to deny the admin row: %v", err)
	}
}

func TestDenyOnTheCreatorRowIsRenOnly(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	assertRejected(t, rig.val.Validate(adminUser, st, deny("creator", tAlpha)), "仅 ren 可编辑")
}

func TestAdminMayDenyDownward(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// admin holds delta itself and moderator is strictly below it: the ordinary
	// delegation path, in the reducing direction.
	if err := rig.val.Validate(adminUser, st, deny("moderator", tDelta)); err != nil {
		t.Errorf("an admin must be able to deny a key it holds, downward: %v", err)
	}
}

// --- Deny: state preconditions ---------------------------------------------

func TestDenyIsRefusedWhenTheKeyIsNotEffective(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// moderator does not hold alpha at all — there is nothing to take away.
	assertRejected(t, rig.val.Validate(renCaller, st, deny("moderator", tAlpha)), "没有可撤销的权限")
}

// TestDenyOnANonDelegableKeyIsRefused pins that the non-delegable set needs no
// deny-specific rule: no editable role holds one of those keys, so the "nothing
// to take away" precondition is what answers, and it answers correctly.
func TestDenyOnANonDelegableKeyIsRefused(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	assertRejected(t, rig.val.Validate(renCaller, st, deny("admin", tLocked)), "没有可撤销的权限")
}

func TestDenyIsRefusedWhenTheKeyComesFromAGrantRow(t *testing.T) {
	rig := newRig()
	st := rig.apply(rows{"moderator": {tAlpha}}, nil)
	// moderator holds alpha only because someone granted it. Writing a deny on
	// top would leave two rows describing one cell; deleting the grant is the
	// operation that means what the operator wants.
	err := rig.val.Validate(renCaller, st, deny("moderator", tAlpha))
	assertRejected(t, err, "来自一条叠加授权")
	assertRejected(t, err, "删除该行")
}

func TestDenyingATwiceDeniedCellIsRefused(t *testing.T) {
	rig := newRig()
	st := rig.apply(nil, rows{"moderator": {tDelta}})
	assertRejected(t, rig.val.Validate(renCaller, st, deny("moderator", tDelta)), "已经是「已撤销」状态")
}

func TestGrantingOnTopOfADenyIsRefused(t *testing.T) {
	rig := newRig()
	st := rig.apply(nil, rows{"moderator": {tDelta}})
	// The cell reads "not held", so a grant looks reasonable — but the unique
	// index makes it impossible and restoring is what the operator means.
	assertRejected(t, rig.val.Validate(renCaller, st, grant("moderator", tDelta)), "请先恢复")
}

// --- Restore (恢复) ---------------------------------------------------------

func TestRestoringWithoutADenyRowIsRefused(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	assertRejected(t, rig.val.Validate(renCaller, st, restore("moderator", tDelta)), "没有可恢复的撤销记录")
}

func TestRestoringADeniedCellIsAllowed(t *testing.T) {
	rig := newRig()
	st := rig.apply(nil, rows{"moderator": {tDelta}})
	if err := rig.val.Validate(renCaller, st, restore("moderator", tDelta)); err != nil {
		t.Errorf("restoring a denied cell must pass: %v", err)
	}
}

// --- Deny: containment holds in the reducing direction too -----------------

func TestDenyRejectedWhenModeratorWouldOutrankAdmin(t *testing.T) {
	rig := newRig()
	st := rig.clean()
	// Both hold delta from code. Cutting admin alone inverts the axis, and the
	// message has to name the role that has to give it up first.
	err := rig.val.Validate(renCaller, st, deny("admin", tDelta))
	assertRejected(t, err, "moderator ⊆ admin")
	assertRejected(t, err, "请先撤销 moderator")
	assertRejected(t, err, string(tDelta))
}

func TestDenyAllowsTheOrderedSequence(t *testing.T) {
	rig := newRig()

	// 1. moderator first — admin keeps delta, so containment holds.
	st := rig.clean()
	if err := rig.val.Validate(renCaller, st, deny("moderator", tDelta)); err != nil {
		t.Fatalf("denying moderator first must pass: %v", err)
	}

	// 2. then admin — now nothing below it holds delta.
	st = rig.apply(nil, rows{"moderator": {tDelta}})
	if err := rig.val.Validate(renCaller, st, deny("admin", tDelta)); err != nil {
		t.Fatalf("denying admin after moderator must pass: %v", err)
	}

	// 3. Unwinding runs the other way round: admin is restored first, exactly
	//    as granting had to go top-down.
	st = rig.apply(nil, rows{"moderator": {tDelta}, "admin": {tDelta}})
	if err := rig.val.Validate(renCaller, st, restore("admin", tDelta)); err != nil {
		t.Fatalf("restoring admin first must pass: %v", err)
	}
	err := rig.val.Validate(renCaller, st, restore("moderator", tDelta))
	assertRejected(t, err, "moderator ⊆ admin")
	assertRejected(t, err, "请先恢复")
}

// TestRestoringRenIsUnreachableEvenWithARow is the validator half of the fuse:
// however a ren deny row got into the table, the console cannot be used to act
// on it, and Merge has already made it inert.
func TestRestoringRenIsUnreachableEvenWithARow(t *testing.T) {
	rig := newRig()
	st := rig.apply(nil, rows{"ren": {tAlpha}})

	if !rig.reg.Effective("ren", tAlpha) {
		t.Fatal("a deny row narrowed ren through Merge")
	}
	assertRejected(t, rig.val.Validate(renCaller, st, restore("ren", tAlpha)), "ren 行不可编辑")
}

func assertRejected(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a rejection mentioning %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("rejection %q does not mention %q", err.Error(), want)
	}
}
