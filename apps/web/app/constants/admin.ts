export const SIDEBAR_MENU = [
  { icon: 'lucide:layout-dashboard', label: '仪表盘', to: '/' },
  { icon: 'lucide:users', label: '用户管理', to: '/users' },
  { icon: 'lucide:globe', label: '站点管理', to: '/sites' },
  { icon: 'lucide:key', label: 'OAuth 客户端', to: '/oauth-clients' },
  { icon: 'lucide:shield', label: '内容审核', to: '/moderation' },
]

export const USER_STATUS_MAP: Record<number, { label: string; color: 'success' | 'danger' | 'default' }> = {
  0: { label: '正常', color: 'success' },
  1: { label: '已封禁', color: 'danger' },
}

export const MODERATION_TABS = [
  { id: 'pending', label: '待审核', icon: 'lucide:clock' },
  { id: 'approved', label: '已通过', icon: 'lucide:check' },
  { id: 'rejected', label: '已拒绝', icon: 'lucide:x' },
]

export const MODERATION_STATUS_MAP: Record<string, { label: string; color: 'warning' | 'success' | 'danger' | 'default' }> = {
  pending: { label: '待审核', color: 'warning' },
  approved: { label: '已通过', color: 'success' },
  rejected: { label: '已拒绝', color: 'danger' },
}
