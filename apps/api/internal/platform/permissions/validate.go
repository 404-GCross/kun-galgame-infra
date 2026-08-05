package permissions

import (
	"fmt"

	"api/internal/platform/authz"
	sitePerm "api/internal/platform/site/perm"
)

// EditableRoles are the only rows the overlay may touch.
//
//   - `user` is excluded because it CANNOT work: a plain user's JWT carries an
//     empty roles array (docs/integration/oauth/11-roles.md), so "user" never
//     reaches Resolver.Can and a row for it would be a grant that silently
//     never fires — or a deny that silently never fires.
//   - `ren` is excluded because it is BOTH the ceiling the containment invariant
//     is measured against and the lockout-recovery fuse. If ren could be widened
//     at runtime, "admin ⊆ ren" would become a moving target; if it could be
//     narrowed, one row could take the console away from the only role that can
//     undo it. Merge enforces the second half independently of this list.
var EditableRoles = []string{RoleCreator, RoleModerator, RoleAdmin}

func isEditableRole(role string) bool {
	for _, r := range EditableRoles {
		if r == role {
			return true
		}
	}
	return false
}

// OverlayState is the effect of every (role, permission) pair that currently has
// an override row. The validator takes it as data rather than querying per cell,
// because the matrix needs a verdict for every cell on the page and that must
// cost one read, not one per cell.
type OverlayState map[string]string

func stateKey(role string, p authz.Permission) string { return role + "\x00" + string(p) }

// Effect returns the overlay effect for (role, p): EffectGrant, EffectDeny, or
// "" when the pair has no row and the code floor decides alone.
func (o OverlayState) Effect(role string, p authz.Permission) string { return o[stateKey(role, p)] }

// HasGrant reports whether the overlay grants (role, p).
func (o OverlayState) HasGrant(role string, p authz.Permission) bool {
	return o.Effect(role, p) == EffectGrant
}

// HasDeny reports whether the overlay denies (role, p).
func (o OverlayState) HasDeny(role string, p authz.Permission) bool {
	return o.Effect(role, p) == EffectDeny
}

// Set records an overlay row.
func (o OverlayState) Set(role string, p authz.Permission, effect string) {
	o[stateKey(role, p)] = effect
}

// Caller is the authenticated console user making the change.
type Caller struct {
	UserID uint
	Roles  []string
}

// Op is one of the four overlay operations. They are four rather than two
// because "revoke" is ambiguous once deny rows exist: deleting a grant row and
// deleting a deny row move a role in opposite directions, and an operator who
// meant one and got the other has no way to tell from the matrix.
type Op string

const (
	// OpGrant writes an EffectGrant row: the role gains a key its bundle lacks.
	OpGrant Op = "grant"
	// OpRevokeGrant deletes an EffectGrant row: back to the code floor.
	OpRevokeGrant Op = "revoke_grant"
	// OpDeny writes an EffectDeny row: the role loses a key its bundle grants.
	OpDeny Op = "deny"
	// OpRevokeDeny deletes an EffectDeny row: back to the code floor.
	OpRevokeDeny Op = "revoke_deny"
)

// Action is one proposed overlay write.
type Action struct {
	Op         Op
	Role       string
	Permission authz.Permission
}

// writesRow reports whether the op INSERTs (as opposed to DELETEs) a row.
func (a Action) writesRow() bool { return a.Op == OpGrant || a.Op == OpDeny }

// effect is the row effect the op operates on.
func (a Action) effect() string {
	if a.Op == OpDeny || a.Op == OpRevokeDeny {
		return EffectDeny
	}
	return EffectGrant
}

// RejectionError is a validation failure with a message meant for the operator
// who clicked the cell — it says which rule stopped them and, where there is
// one, the move that would work instead.
type RejectionError struct{ Msg string }

func (e *RejectionError) Error() string { return e.Msg }

func reject(format string, args ...any) error {
	return &RejectionError{Msg: fmt.Sprintf(format, args...)}
}

// Validator enforces the overlay write rules against a registry.
type Validator struct{ reg *Registry }

// NewValidator wires a validator over reg.
func NewValidator(reg *Registry) *Validator { return &Validator{reg: reg} }

// ManagesPermissions reports whether the caller holds oauth.permissions.manage
// — the ren-only key that lifts the delegation rule (but not the invariants).
func ManagesPermissions(roles []string) bool {
	return sitePerm.Resolver.Can(roles, sitePerm.PermissionsManage)
}

// Validate applies every rule to one proposed write and returns nil only when
// all of them pass. It is the single authority: the matrix's editable-cell map
// is computed by running this same function per cell, so the UI can never
// disagree with the write path about what is allowed.
func (v *Validator) Validate(caller Caller, state OverlayState, act Action) error {
	// Rule 1a — the target row must be one the overlay may touch. This is where
	// ren's immunity is stated to the operator; Merge states it to the engine.
	if !isEditableRole(act.Role) {
		if act.Role == RoleUser {
			return reject("user 行不可编辑:普通用户的 JWT roles 为空数组,授予或撤销它的权限都不会生效")
		}
		if act.Role == RoleRen {
			return reject("ren 行不可编辑:ren 既是包含性不变量的上界,也是锁死后的恢复保险,只能改代码捆")
		}
		return reject("未知角色 %q,只能编辑 creator / moderator / admin", act.Role)
	}

	// Rule 1b — the key must be live. This is what keeps a typo, or a key from
	// the retired galgame vocabulary, out of the table.
	if _, ok := v.reg.Lookup(act.Permission); !ok {
		return reject("未知权限键 %q", act.Permission)
	}

	// Rule 3 — non-delegable keys are never GRANTABLE through the overlay, by
	// anyone. Checked before the delegation rule so even a permissions.manage
	// holder gets this answer. It says nothing about denying: refusing to hand a
	// key out is a rule about escalation, and no editable role holds one of
	// these keys anyway — so a deny attempt is caught below, by the precondition
	// that there is nothing to take away.
	if act.Op == OpGrant && v.reg.IsNonDelegable(act.Permission) {
		return reject("%q 不可委派:此类权限只能通过改代码捆并部署来变更", act.Permission)
	}

	// Rule 2 — delegation, for callers who do not hold oauth.permissions.manage.
	// Identical for grants and denies: taking a key off a peer is no less of a
	// privileged act than handing one to them. Note what the strictly-below rule
	// already implies — nobody can act on their own tier, so an admin can never
	// deny the admin row, and only ren (which lifts this rule) can.
	if !ManagesPermissions(caller.Roles) {
		if act.Role == RoleCreator {
			return reject("creator 行仅 ren 可编辑")
		}
		if HighestRank([]string{act.Role}) >= HighestRank(caller.Roles) {
			return reject("只能编辑严格低于自己管理层级的角色(你的层级不高于 %q)", act.Role)
		}
		if !v.reg.holderCan(caller.Roles, act.Permission) {
			if act.Op == OpDeny {
				return reject("不能撤销自己都不持有的权限 %q", act.Permission)
			}
			return reject("不能授予自己都不持有的权限 %q", act.Permission)
		}
	}

	if err := v.checkState(state, act); err != nil {
		return err
	}

	// Rule 4 — containment. moderator ⊆ admin ⊆ ren must still hold for this
	// key after the write. creator is off the management axis and is not part
	// of the chain.
	return v.checkContainment(state, act)
}

// checkState is the precondition half: does the cell actually look the way this
// operation assumes? Each failure names the operation that WOULD work, because
// every one of these is a case where the operator wants something reachable and
// picked the wrong door.
func (v *Validator) checkState(state OverlayState, act Action) error {
	effective := v.reg.Effective(act.Role, act.Permission)
	hasGrant := state.HasGrant(act.Role, act.Permission)
	hasDeny := state.HasDeny(act.Role, act.Permission)

	switch act.Op {
	case OpGrant:
		if hasDeny {
			return reject("%q 的 %q 目前是「已撤销」状态:请先恢复这条撤销记录,而不是新增授予",
				act.Role, act.Permission)
		}
		if effective {
			return reject("%q 已持有 %q,无需重复授予", act.Role, act.Permission)
		}

	case OpRevokeGrant:
		if !hasGrant {
			if effective {
				return reject("%q 的 %q 来自代码捆,没有叠加授权可删:要收回它请使用「撤销(记 deny)」",
					act.Role, act.Permission)
			}
			return reject("%q 并未持有 %q,没有可撤销的叠加授权", act.Role, act.Permission)
		}

	case OpDeny:
		if hasDeny {
			return reject("%q 的 %q 已经是「已撤销」状态", act.Role, act.Permission)
		}
		if hasGrant {
			return reject("%q 的 %q 来自一条叠加授权:请直接撤销这条叠加授权(删除该行),而不是再记一条 deny",
				act.Role, act.Permission)
		}
		if !effective {
			return reject("%q 并未持有 %q,没有可撤销的权限", act.Role, act.Permission)
		}

	case OpRevokeDeny:
		if !hasDeny {
			return reject("%q 的 %q 没有可恢复的撤销记录", act.Role, act.Permission)
		}

	default:
		return reject("未知操作 %q", act.Op)
	}
	return nil
}

// effectiveAfter is what the acting role holds once the write lands. Both
// deletions return the role to its code floor — which is the honest answer for
// either row kind, and the reason this is not simply "the op added something".
func (v *Validator) effectiveAfter(act Action) bool {
	switch act.Op {
	case OpGrant:
		return true
	case OpDeny:
		return false
	default:
		return v.reg.InCodeBundle(act.Role, act.Permission)
	}
}

// checkContainment evaluates the post-write effective set of the single key
// being written across the management axis. It takes the state as well as the
// action because "fix the other end first" is only useful advice if it names
// the operation that would do it — and which one that is depends on whether the
// other end's cell is a code cell, a grant row or a deny row.
func (v *Validator) checkContainment(state OverlayState, act Action) error {
	after := func(role string) bool {
		if role == act.Role {
			return v.effectiveAfter(act)
		}
		return v.reg.Effective(role, act.Permission)
	}
	mod, admin, ren := after(RoleModerator), after(RoleAdmin), after(RoleRen)

	if mod && !admin {
		// Either moderator was just raised, or admin was just lowered — which of
		// the two decides which end the operator has to fix first.
		if act.Role == RoleAdmin {
			how := "记 deny"
			if state.HasGrant(RoleModerator, act.Permission) {
				how = "删除那条叠加授权"
			}
			return reject("此操作会破坏 moderator ⊆ admin:请先撤销 moderator 的 %q(%s)",
				act.Permission, how)
		}
		if state.HasDeny(RoleAdmin, act.Permission) {
			return reject("此操作会破坏 moderator ⊆ admin:admin 的 %q 目前处于已撤销状态,请先恢复它",
				act.Permission)
		}
		return reject("此操作会破坏 moderator ⊆ admin:请先把 %q 授予 admin", act.Permission)
	}
	if admin && !ren {
		// ren can never be narrowed by the overlay, so this can only be a grant
		// to admin of a key ren's bundle lacks — a genuine hierarchy inversion,
		// fixable only in code.
		return reject("授予会破坏 admin ⊆ ren:ren 的代码捆不含 %q", act.Permission)
	}
	return nil
}

// holderCan asks the owning domain's live resolver — code bundles ∪ overlay —
// whether these roles hold p.
func (r *Registry) holderCan(roles []string, p authz.Permission) bool {
	d, ok := r.Lookup(p)
	if !ok {
		return false
	}
	return d.Holder.Can(roles, p)
}
