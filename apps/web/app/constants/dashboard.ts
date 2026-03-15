export const DASHBOARD_STATS = [
  { label: '用户总数', value: '0', icon: 'lucide:users', color: 'bg-blue-500' },
  { label: '活跃会话', value: '0', icon: 'lucide:activity', color: 'bg-green-500' },
  { label: '站点数量', value: '0', icon: 'lucide:globe', color: 'bg-purple-500' },
  { label: 'OAuth 客户端', value: '0', icon: 'lucide:key', color: 'bg-orange-500' },
]

export const QUICK_ACTIONS = [
  { label: '用户管理', to: '/users', icon: 'lucide:user-plus', color: 'text-blue-500' },
  { label: '站点设置', to: '/sites', icon: 'lucide:settings', color: 'text-purple-500' },
  { label: 'OAuth 客户端', to: '/oauth-clients', icon: 'lucide:key', color: 'text-orange-500' },
  { label: '内容审核', to: '/moderation', icon: 'lucide:shield', color: 'text-green-500' },
]
