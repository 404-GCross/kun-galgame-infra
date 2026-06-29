export const SIDEBAR_MENU = [
  { icon: 'lucide:layout-dashboard', label: '仪表盘', to: '/' },
  { icon: 'lucide:gamepad-2', label: 'Galgame', to: '/galgame' },
  { icon: 'lucide:calendar-days', label: '发售月历', to: '/galgame/calendar' },
  { icon: 'lucide:inbox', label: '审核队列', to: '/review' },
  { icon: 'lucide:tag', label: '标签', to: '/tag' },
  { icon: 'lucide:building', label: '会社', to: '/official' },
  { icon: 'lucide:library', label: '系列', to: '/series' },
  { icon: 'lucide:cpu', label: '引擎', to: '/engine' }
]

export const GALGAME_STATUS_MAP: Record<
  number,
  {
    label: string
    color: 'success' | 'warning' | 'danger' | 'default' | 'primary'
  }
> = {
  0: { label: '已发布', color: 'success' },
  1: { label: '已封禁', color: 'danger' },
  2: { label: 'VNDB 草稿', color: 'warning' },
  3: { label: '待审核', color: 'primary' },
  4: { label: '已拒绝', color: 'default' }
}

// Tabs for the review queue page (/review). Each tab maps to a `type`
// filter on GET /admin/galgame/messages. See
// docs/integration/galgame_wiki/08-messages.md for the API spec.
export const REVIEW_QUEUE_TABS = [
  {
    id: 'all_pending',
    label: '全部待审',
    icon: 'lucide:inbox',
    types: 'submitted,edited_pending'
  },
  {
    id: 'submitted',
    label: '新提交',
    icon: 'lucide:file-plus',
    types: 'submitted'
  },
  {
    id: 'edited_pending',
    label: '已修订',
    icon: 'lucide:pencil-line',
    types: 'edited_pending'
  }
] as const

// Allowed admin transitions on PUT /admin/galgame/:gid/status (target 0/1/4).
// `needs_reason` drives whether a reason input is shown in the action modal.
export const REVIEW_ACTIONS: Array<{
  id: 'approve' | 'decline' | 'ban'
  label: string
  icon: string
  color: 'success' | 'danger' | 'warning'
  status: 0 | 1 | 4
  needs_reason: boolean
}> = [
  {
    id: 'approve',
    label: '通过审核',
    icon: 'lucide:circle-check',
    color: 'success',
    status: 0,
    needs_reason: false
  },
  {
    id: 'decline',
    label: '拒绝（可重新提交）',
    icon: 'lucide:circle-x',
    color: 'danger',
    status: 4,
    needs_reason: true
  },
  {
    id: 'ban',
    label: '封禁此条目',
    icon: 'lucide:ban',
    color: 'warning',
    status: 1,
    needs_reason: true
  }
]

export const GALGAME_STATUS_TABS = [
  { id: 0, label: '已发布', icon: 'lucide:circle-check' },
  { id: 2, label: '草稿 (VNDB)', icon: 'lucide:file-pen-line' },
  { id: 1, label: '已封禁', icon: 'lucide:ban' }
] as const

export const CONTENT_LIMIT_MAP: Record<
  string,
  { label: string; color: 'success' | 'danger' | 'default' }
> = {
  sfw: { label: 'SFW', color: 'success' },
  nsfw: { label: 'NSFW', color: 'danger' }
}

export const TAG_CATEGORY_MAP: Record<
  string,
  { label: string; color: 'primary' | 'secondary' | 'info' | 'default' }
> = {
  content: { label: '内容', color: 'primary' },
  sexual: { label: '情色', color: 'secondary' },
  technical: { label: '技术', color: 'info' }
}

export const OFFICIAL_CATEGORY_MAP: Record<
  string,
  { label: string; color: 'primary' | 'info' | 'default' }
> = {
  company: { label: '公司', color: 'primary' },
  individual: { label: '个人', color: 'info' },
  amateur: { label: '同人', color: 'default' }
}

export const ADMIN_STATS_ENTITIES: Array<{
  key: keyof import('~/shared/types/galgame').AdminStatsTotals
  label: string
  icon: string
  color: string
}> = [
  { key: 'galgame_tag', label: '标签', icon: 'lucide:tag', color: 'text-primary' },
  { key: 'galgame_official', label: '会社', icon: 'lucide:building', color: 'text-secondary' },
  { key: 'galgame_engine', label: '引擎', icon: 'lucide:cpu', color: 'text-info' },
  { key: 'galgame_series', label: '系列', icon: 'lucide:library', color: 'text-success' },
  { key: 'galgame_link', label: '链接', icon: 'lucide:link', color: 'text-warning' },
  { key: 'galgame_pr', label: 'PR', icon: 'lucide:git-pull-request', color: 'text-danger' },
  { key: 'galgame_revision', label: 'Revision', icon: 'lucide:history', color: 'text-default-500' }
]
