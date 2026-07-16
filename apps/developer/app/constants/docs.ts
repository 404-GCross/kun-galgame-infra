// Presentation vocabulary for the self-built API reference (/docs/**). The
// documentation data itself is the generated docs model (app/generated/
// docs-model.ts); this file only maps model values to UI (badge colors, icons).
import type { DocsMethod, DocsFaceKey } from '~~/shared/types/docs'

// HTTP method → badge classes. Semantic palette only (no gradients): GET reads
// as safe/success, POST as a primary action, PUT/PATCH as a caution, DELETE as
// danger. Full literal class strings so Tailwind's JIT scanner keeps them.
export const DOCS_METHOD_BADGE: Record<DocsMethod, string> = {
  get: 'bg-success-50 text-success-600',
  post: 'bg-primary-50 text-primary-600',
  put: 'bg-warning-50 text-warning-600',
  patch: 'bg-warning-50 text-warning-600',
  delete: 'bg-danger-50 text-danger-600'
}

// Per-face chrome for the docs home + face overview cards.
export const DOCS_FACE_META: Record<
  DocsFaceKey,
  { icon: string; tagline: string }
> = {
  galgame: {
    icon: 'lucide:gamepad-2',
    tagline:
      '聚合记录：多源归并的名称 / 简介 / 封面 / 标签 / 会社 / 发售 / 三源评分，含变更流增量同步。'
  },
  catalog: {
    icon: 'lucide:network',
    tagline:
      '跨媒介身份图谱：作品 / 人物名义 / 角色 / 厂牌 / credits / 关系，外部 id 反查四源锚。'
  }
}
