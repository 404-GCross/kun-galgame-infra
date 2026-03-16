export const DASHBOARD_STATS = [
  { label: '用户总数', value: '0', icon: 'lucide:users', color: 'bg-primary' },
  { label: '活跃会话', value: '0', icon: 'lucide:activity', color: 'bg-success' },
  { label: '站点数量', value: '0', icon: 'lucide:globe', color: 'bg-default' },
  { label: 'OAuth 客户端', value: '0', icon: 'lucide:key', color: 'bg-warning' },
]

export const QUICK_ACTIONS = [
  { label: '用户管理', to: '/users', icon: 'lucide:user-plus', color: 'text-primary' },
  { label: '站点设置', to: '/sites', icon: 'lucide:settings', color: 'text-default' },
  { label: 'OAuth 客户端', to: '/oauth-clients', icon: 'lucide:key', color: 'text-warning' },
  { label: '内容审核', to: '/moderation', icon: 'lucide:shield', color: 'text-success' },
]
