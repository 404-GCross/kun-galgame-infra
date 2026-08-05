// Permission console types — the matrix export and its overlay writes, served
// by cmd/oauth under /api/v1/admin/permissions/*.
//
// Hand-written (like site.ts) rather than generated: these endpoints are house
// Fiber handlers, not a Huma/OpenAPI face, so there is no spec to generate
// from. They mirror internal/platform/permissions/service.go exactly.

// What a cell is. `code` is the compiled-in bundle; `grant` and `deny` are the
// two kinds of role_permission_overrides row — one adds a key the bundle lacks,
// the other takes away one it has.
//
// Each state admits exactly ONE operation, which is why nothing here has to
// choose between two buttons: none → 授予, grant → 撤销叠加授权, code → 撤销(记
// deny), deny → 恢复.
export type PermissionGrantSource = 'none' | 'code' | 'grant' | 'deny'

export interface PermissionCell {
  granted: boolean
  source: PermissionGrantSource
  // The three verdicts below are decided server-side by the same validator the
  // write path runs, so the UI never re-implements authorization — it only
  // renders the answer. At most one is ever true.
  //
  // editable: this caller may add (source none) or remove (source grant) a
  // grant row here.
  editable: boolean
  // can_deny: this caller may write a deny row on this code-floor cell.
  can_deny: boolean
  // can_restore: this caller may delete the deny row, returning the role to the
  // code floor.
  can_restore: boolean
  // Why a cell offers no operation at all (absent when one is available).
  reason?: string
}

export interface PermissionKeyRow {
  key: string
  desc_en: string
  desc_zh: string
  // Keys the overlay may never grant, by anyone — they move only in code.
  non_delegable: boolean
  // Keyed by role name; every role in PermissionMatrix.roles is present.
  grants: Record<string, PermissionCell>
}

export interface PermissionDomainView {
  name: string
  title_zh: string
  keys: PermissionKeyRow[]
}

export interface PermissionMatrix {
  roles: string[]
  // The rows the overlay may ever touch (creator / moderator / admin); the
  // others render greyed.
  editable_roles: string[]
  manages_permissions: boolean
  domains: PermissionDomainView[]
}

// `revoke` predates the deny half and still means "a grant row was deleted";
// renaming it would rewrite the meaning of every historical audit row.
export type PermissionAuditAction = 'grant' | 'revoke' | 'deny' | 'revoke_deny'

// The `effect` of POST /admin/permissions/overrides. The DELETE takes none: the
// row that is there is the row it removes, and letting the client assert which
// one invites deleting a deny while believing it revoked a grant.
export type PermissionEffect = 'grant' | 'deny'

export interface PermissionAuditEntry {
  id: number
  actor_user_id: number
  actor_name: string
  action: PermissionAuditAction
  role: string
  permission: string
  created_at: string
}
