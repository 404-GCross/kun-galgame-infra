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
//     never fires.
//   - `ren` is excluded because it is the ceiling the containment invariant is
//     measured against. If ren could be widened at runtime, "admin ⊆ ren" would
//     become a moving target and the console could grant itself upward.
var EditableRoles = []string{RoleCreator, RoleModerator, RoleAdmin}

func isEditableRole(role string) bool {
	for _, r := range EditableRoles {
		if r == role {
			return true
		}
	}
	return false
}

// OverlayState is which (role, permission) pairs currently have an override
// row. The validator takes it as data rather than querying per cell, because
// the matrix needs an editable/not-editable verdict for every cell on the page
// and that must cost one read, not one per cell.
type OverlayState map[string]bool

func stateKey(role string, p authz.Permission) string { return role + "\x00" + string(p) }

// Has reports whether the overlay grants (role, p).
func (o OverlayState) Has(role string, p authz.Permission) bool { return o[stateKey(role, p)] }

// Set records an overlay grant.
func (o OverlayState) Set(role string, p authz.Permission) { o[stateKey(role, p)] = true }

// Caller is the authenticated console user making the change.
type Caller struct {
	UserID uint
	Roles  []string
}

// Action is one proposed overlay write.
type Action struct {
	// Grant is true for a grant, false for a revoke (deleting the row).
	Grant      bool
	Role       string
	Permission authz.Permission
}

// RejectionError is a validation failure with a message meant for the operator
// who clicked the cell — it says which rule stopped them and, where there is
// one, the move that would work instead.
type RejectionError struct{ Msg string }

func (e *RejectionError) Error() string { return e.Msg }

func reject(format string, args ...any) error {
	return &RejectionError{Msg: fmt.Sprintf(format, args...)}
}

// Validator enforces the five overlay write rules against a registry.
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
	// Rule 1a — the target row must be one the overlay may touch.
	if !isEditableRole(act.Role) {
		if act.Role == RoleUser {
			return reject("user 行不可编辑:普通用户的 JWT roles 为空数组,授予它的权限永远不会生效")
		}
		if act.Role == RoleRen {
			return reject("ren 行不可编辑:ren 是包含性不变量的上界,只能改代码捆")
		}
		return reject("未知角色 %q,只能编辑 creator / moderator / admin", act.Role)
	}

	// Rule 1b — the key must be live. This is what keeps a typo, or a key from
	// the retired galgame vocabulary, out of the table.
	if _, ok := v.reg.Lookup(act.Permission); !ok {
		return reject("未知权限键 %q", act.Permission)
	}

	// Rule 3 — non-delegable keys are never grantable through the overlay, by
	// anyone. Checked before the delegation rule so even a permissions.manage
	// holder gets this answer.
	if v.reg.IsNonDelegable(act.Permission) {
		return reject("%q 不可委派:此类权限只能通过改代码捆并部署来变更", act.Permission)
	}

	// Rule 2 — delegation, for callers who do not hold oauth.permissions.manage.
	if !ManagesPermissions(caller.Roles) {
		if act.Role == RoleCreator {
			return reject("creator 行仅 ren 可编辑")
		}
		if HighestRank([]string{act.Role}) >= HighestRank(caller.Roles) {
			return reject("只能编辑严格低于自己管理层级的角色(你的层级不高于 %q)", act.Role)
		}
		if !v.reg.holderCan(caller.Roles, act.Permission) {
			return reject("不能授予自己都不持有的权限 %q", act.Permission)
		}
	}

	// The state precondition. A grant that is already effective is a no-op, and
	// a revoke with no overlay row would be a request to cut BELOW the code
	// floor — which the model has no way to express and deliberately refuses.
	if act.Grant {
		if v.reg.Effective(act.Role, act.Permission) {
			return reject("%q 已持有 %q,无需重复授予", act.Role, act.Permission)
		}
	} else if !state.Has(act.Role, act.Permission) {
		return reject("%q 的 %q 来自代码捆,叠加层无法撤销(代码捆是地板)", act.Role, act.Permission)
	}

	// Rule 4 — containment. moderator ⊆ admin ⊆ ren must still hold for this
	// key after the write. creator is off the management axis and is not part
	// of the chain.
	return v.checkContainment(act)
}

// checkContainment evaluates the post-write effective set of the single key
// being written across the management axis.
func (v *Validator) checkContainment(act Action) error {
	after := func(role string) bool {
		if role == act.Role {
			return act.Grant
		}
		return v.reg.Effective(role, act.Permission)
	}
	mod, admin, ren := after(RoleModerator), after(RoleAdmin), after(RoleRen)

	if mod && !admin {
		if act.Grant {
			return reject("授予会破坏 moderator ⊆ admin:请先把 %q 授予 admin", act.Permission)
		}
		return reject("撤销会破坏 moderator ⊆ admin:请先撤销 moderator 的 %q", act.Permission)
	}
	if admin && !ren {
		// ren is code-only, so this can only be a grant to admin of a key ren's
		// bundle lacks — a genuine hierarchy inversion, fixable only in code.
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
