import type { PermissionAuditAction } from '~~/shared/types/permission'

// UI-only maps for the permission matrix. The backend owns every authorization
// decision (including which cells this caller may edit) — nothing here decides
// anything, it only labels and colors.

// Chip colors mirror @kungal/ui-core's KunUIColor union — project palette only,
// never Tailwind built-ins.
export type PermissionChipColor =
  | 'default'
  | 'primary'
  | 'secondary'
  | 'success'
  | 'warning'
  | 'danger'
  | 'info'

export const AUDIT_ACTION_LABELS: Record<PermissionAuditAction, string> = {
  grant: '授予',
  revoke: '撤销'
}

export const AUDIT_ACTION_COLORS: Record<
  PermissionAuditAction,
  PermissionChipColor
> = {
  grant: 'success',
  revoke: 'warning'
}

// How many audit rows the "最近变更" list asks for.
export const AUDIT_LIST_LIMIT = 20

// Why the two immutable columns are immutable. Shown as the column note so an
// operator does not read a greyed column as a bug.
export const IMMUTABLE_ROLE_NOTES: Record<string, string> = {
  user: '普通用户的 JWT roles 为空数组，授予它的权限永远不会生效',
  ren: 'ren 是包含性不变量的上界，只能通过改代码捆并部署来变更'
}
