// Display labels for the unified moemoepoint ledger `reason` enum
// (OAuth-owned; see kun-oauth-admin/docs/integration/oauth/06-moemoepoint.md §2).
export const MOEMOEPOINT_REASON_LABEL: Record<string, string> = {
  admin_grant: '管理员发放',
  admin_deduct: '管理员扣除',
  migration: '迁移',
  content_approved: '内容采纳',
  content_removed: '内容下架',
  daily_checkin: '签到',
  liked: '被点赞'
}

export const moemoepointReasonLabel = (reason: string): string =>
  MOEMOEPOINT_REASON_LABEL[reason] ?? reason
