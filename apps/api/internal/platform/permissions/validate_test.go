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
//	ren   : locked, alpha, beta
//	admin : alpha
//	mod   : —
//
// gamma is deliberately in NO bundle: it is the key that proves the admin ⊆ ren
// half of containment can actually fail.
const (
	tAlpha  authz.Permission = "demo.alpha"
	tBeta   authz.Permission = "demo.beta"
	tGamma  authz.Permission = "demo.gamma"
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
		"moderator": {},
		"admin":     {tAlpha},
		"ren":       {tAlpha, tBeta, tLocked},
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
			{Permission: tLocked, DescEN: "locked", DescZH: "locked"},
		},
	})
	return &testRig{
		reg: reg, holder: holder, base: base,
		own: map[authz.Permission]bool{tAlpha: true, tBeta: true, tGamma: true, tLocked: true},
		val: permissions.NewValidator(reg),
	}
}

// apply installs an overlay state as if it had been written, so containment and
// revoke rules can be exercised against a realistic starting point.
func (r *testRig) apply(snap permissions.Snapshot) permissions.OverlayState {
	r.holder.Swap(authz.NewResolver(permissions.Merge(r.base, snap, r.own)))
	st := permissions.OverlayState{}
	for role, perms := range snap {
		for _, p := range perms {
			st.Set(role, p)
		}
	}
	return st
}

func grant(role string, p authz.Permission) permissions.Action {
	return permissions.Action{Grant: true, Role: role, Permission: p}
}

func revoke(role string, p authz.Permission) permissions.Action {
	return permissions.Action{Grant: false, Role: role, Permission: p}
}

var (
	renCaller = permissions.Caller{UserID: 1, Roles: []string{"admin", "ren"}}
	adminUser = permissions.Caller{UserID: 2, Roles: []string{"admin"}}
	modCaller = permissions.Caller{UserID: 3, Roles: []string{"moderator"}}
)

// --- Rule 1: known key, editable role -------------------------------------

func TestRule1RejectsImmutableAndUnknownRoles(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})

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
	st := rig.apply(permissions.Snapshot{})
	err := rig.val.Validate(renCaller, st, grant("moderator", authz.Permission("demo.nonexistent")))
	assertRejected(t, err, "未知权限键")
}

// --- Rule 3: non-delegable keys -------------------------------------------

func TestRule3NonDelegableIsRefusedEvenForRen(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})
	// ren holds oauth.permissions.manage, so this caller bypasses delegation —
	// and is still refused.
	err := rig.val.Validate(renCaller, st, grant("admin", tLocked))
	assertRejected(t, err, "不可委派")
}

// --- Rule 2: delegation ----------------------------------------------------

func TestRule2TargetMustBeStrictlyBelowCaller(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})

	// admin → admin: same tier, refused.
	assertRejected(t, rig.val.Validate(adminUser, st, grant("admin", tBeta)), "严格低于")
	// moderator → moderator: same tier, refused.
	assertRejected(t, rig.val.Validate(modCaller, st, grant("moderator", tAlpha)), "严格低于")
}

func TestRule2CreatorRowIsRenOnly(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})

	assertRejected(t, rig.val.Validate(adminUser, st, grant("creator", tAlpha)), "仅 ren 可编辑")

	// ren may edit it — creator is off the management axis, so containment
	// does not constrain the grant.
	if err := rig.val.Validate(renCaller, st, grant("creator", tAlpha)); err != nil {
		t.Errorf("ren must be able to grant on the creator row: %v", err)
	}
}

func TestRule2CallerMustHoldThePermission(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})

	// admin holds alpha but not beta.
	assertRejected(t, rig.val.Validate(adminUser, st, grant("moderator", tBeta)), "自己都不持有")

	if err := rig.val.Validate(adminUser, st, grant("moderator", tAlpha)); err != nil {
		t.Errorf("admin delegating a key it holds, downward, must pass: %v", err)
	}
}

func TestRule2IsLiftedByPermissionsManage(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{"admin": {tBeta}})

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
	st := rig.apply(permissions.Snapshot{})
	assertRejected(t, rig.val.Validate(renCaller, st, grant("admin", tAlpha)), "无需重复授予")
}

func TestRevokingACodeFloorGrantIsRefused(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})
	// admin holds alpha from the CODE bundle, not the overlay: there is no row
	// to delete, and the model has no way to cut below the floor.
	assertRejected(t, rig.val.Validate(renCaller, st, revoke("admin", tAlpha)), "代码捆是地板")
}

func TestRevokingAnOverlayGrantIsAllowed(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{"admin": {tBeta}})
	if err := rig.val.Validate(renCaller, st, revoke("admin", tBeta)); err != nil {
		t.Errorf("revoking an overlay grant must pass: %v", err)
	}
}

// --- Rule 4: containment ---------------------------------------------------

func TestRule4GrantRejectedWhenAdminLacksTheKey(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})
	// moderator ⊆ admin would break: admin does not hold beta.
	err := rig.val.Validate(renCaller, st, grant("moderator", tBeta))
	assertRejected(t, err, "请先把")
	assertRejected(t, err, "授予 admin")
}

func TestRule4GrantRejectedWhenRenLacksTheKey(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{})
	// admin ⊆ ren would break: gamma is in no bundle at all, so ren lacks it
	// and no overlay row can put it there (ren rows are immutable).
	assertRejected(t, rig.val.Validate(renCaller, st, grant("admin", tGamma)), "ren 的代码捆不含")
}

func TestRule4RevokeRejectedWhenModeratorStillHoldsTheKey(t *testing.T) {
	rig := newRig()
	st := rig.apply(permissions.Snapshot{"admin": {tBeta}, "moderator": {tBeta}})
	// Cutting admin while moderator keeps it inverts the axis.
	err := rig.val.Validate(renCaller, st, revoke("admin", tBeta))
	assertRejected(t, err, "请先撤销 moderator")
}

func TestRule4AllowsTheOrderedSequence(t *testing.T) {
	rig := newRig()

	// 1. Grant admin first — allowed, ren holds beta.
	st := rig.apply(permissions.Snapshot{})
	if err := rig.val.Validate(renCaller, st, grant("admin", tBeta)); err != nil {
		t.Fatalf("granting admin first must pass: %v", err)
	}

	// 2. Then moderator — now allowed.
	st = rig.apply(permissions.Snapshot{"admin": {tBeta}})
	if err := rig.val.Validate(renCaller, st, grant("moderator", tBeta)); err != nil {
		t.Fatalf("granting moderator after admin must pass: %v", err)
	}

	// 3. Unwinding works in the reverse order: moderator first.
	st = rig.apply(permissions.Snapshot{"admin": {tBeta}, "moderator": {tBeta}})
	if err := rig.val.Validate(renCaller, st, revoke("moderator", tBeta)); err != nil {
		t.Fatalf("revoking moderator first must pass: %v", err)
	}
	st = rig.apply(permissions.Snapshot{"admin": {tBeta}})
	if err := rig.val.Validate(renCaller, st, revoke("admin", tBeta)); err != nil {
		t.Fatalf("revoking admin after moderator must pass: %v", err)
	}
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
