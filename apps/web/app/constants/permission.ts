import type {
  PermissionAuditAction,
  PermissionCell
} from '~~/shared/types/permission'

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
  revoke: '撤销授予',
  deny: '撤销权限',
  revoke_deny: '恢复权限'
}

export const AUDIT_ACTION_COLORS: Record<
  PermissionAuditAction,
  PermissionChipColor
> = {
  grant: 'success',
  revoke: 'warning',
  // A deny is the only action that leaves a role with LESS than its code
  // bundle, so it is the only one that reads as danger.
  deny: 'danger',
  revoke_deny: 'info'
}

// The four cell operations, as the console names them to an operator. Which one
// a cell offers follows from its source; the server has already said whether
// this caller may perform it.
export type PermissionCellOp = 'grant' | 'revoke' | 'deny' | 'restore'

export const CELL_OP_LABELS: Record<PermissionCellOp, string> = {
  grant: '授予',
  revoke: '撤销叠加授权',
  deny: '撤销（记 deny）',
  restore: '恢复'
}

// cellOp maps a cell to the ONE operation it admits, or null when it admits
// none — either because the row is immutable or because this caller may not.
// The backend decided the "may"; this only reads which door the cell has.
export const cellOp = (cell: PermissionCell): PermissionCellOp | null => {
  if (cell.editable) return cell.source === 'grant' ? 'revoke' : 'grant'
  if (cell.can_deny) return 'deny'
  if (cell.can_restore) return 'restore'
  return null
}

// Every operation lands on every holder of the role at once, and the services
// pick the change up within one poll interval. An operator clicking a cell sees
// a tick change; this is what actually happens.
export const BLAST_RADIUS_NOTE = '作用于所有持有该角色的用户，≤30 秒生效'

// How many audit rows the "最近变更" list asks for.
export const AUDIT_LIST_LIMIT = 20

// Why the two immutable columns are immutable. Shown as the column note so an
// operator does not read a greyed column as a bug.
export const IMMUTABLE_ROLE_NOTES: Record<string, string> = {
  user: '普通用户的 JWT roles 为空数组，授予它的权限永远不会生效',
  ren: 'ren 既是包含性不变量的上界，也是锁死后的恢复保险：任何叠加行都不能削减它，只能改代码捆并部署'
}
