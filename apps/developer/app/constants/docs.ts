import type { DocsMethod, DocsFaceKey } from '~~/shared/types/docs'

export const DOCS_METHOD_BADGE: Record<DocsMethod, string> = {
  get: 'bg-success-50 text-success-600',
  post: 'bg-primary-50 text-primary-600',
  put: 'bg-warning-50 text-warning-600',
  patch: 'bg-warning-50 text-warning-600',
  delete: 'bg-danger-50 text-danger-600'
}

export const DOCS_FACE_META: Record<
  DocsFaceKey,
  { icon: string; tagline: string }
> = {
  catalog: {
    icon: 'lucide:network',
    tagline:
      '跨媒介身份正典：作品 / 人物名义 / 角色 / 厂牌 / credits / 关系，外部 id 反查四源锚。'
  },
  playtime: {
    icon: 'lucide:timer',
    tagline:
      '用户自己的游玩时长：上报（单条 / 外部 id 寻址 / 批量）与回拉。走用户访问令牌，不是 API 密钥。'
  }
}
