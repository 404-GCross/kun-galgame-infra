// Permission console types — the matrix export and its overlay writes, served
// by cmd/oauth under /api/v1/admin/permissions/*.
//
// Hand-written (like site.ts) rather than generated: these endpoints are house
// Fiber handlers, not a Huma/OpenAPI face, so there is no spec to generate
// from. They mirror internal/platform/permissions/service.go exactly.

// Where a grant comes from. `code` is the compiled-in bundle — the floor, which
// the overlay can widen but never cut — and `overlay` is a
// role_permission_overrides row, the only kind that can be revoked here.
export type PermissionGrantSource = 'none' | 'code' | 'overlay'

export interface PermissionCell {
  granted: boolean
  source: PermissionGrantSource
  // Decided server-side by the same validator the write path runs, so the UI
  // never re-implements authorization — it only renders the verdict.
  editable: boolean
  // Why a cell is not editable (absent when it is).
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

export type PermissionAuditAction = 'grant' | 'revoke'

export interface PermissionAuditEntry {
  id: number
  actor_user_id: number
  actor_name: string
  action: PermissionAuditAction
  role: string
  permission: string
  created_at: string
}
