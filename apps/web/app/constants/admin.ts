export const SIDEBAR_MENU = [
  { icon: 'lucide:layout-dashboard', label: '仪表盘', to: '/', adminOnly: true },
  { icon: 'lucide:users', label: '用户管理', to: '/users', adminOnly: true },
  { icon: 'lucide:globe', label: '站点管理', to: '/sites', adminOnly: true },
  { icon: 'lucide:key', label: 'OAuth 客户端', to: '/oauth-clients', adminOnly: true },
  { icon: 'lucide:shield', label: '内容审核', to: '/moderation', adminOnly: true },
  { icon: 'lucide:user', label: '个人信息', to: '/profile' },
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
