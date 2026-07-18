// Display labels for the unified moemoepoint ledger `reason` enum
// (OAuth-owned; see kun-galgame-infra/docs/integration/oauth/06-moemoepoint.md §2).
// `reason` is a small stable enum; the concrete business detail lives in
// `source_app` (the awarding site) + `ref` ("type:id", e.g. "galgame:1207").

// Static labels for reasons whose meaning doesn't depend on the triggering entity.
const STATIC_REASON_LABEL: Record<string, string> = {
  admin_grant: '管理员发放',
  admin_deduct: '管理员扣除',
  migration: '积分迁移',
  register_gift: '注册欢迎礼',
  daily_checkin: '每日签到',
  content_removed: '内容被下架'
}

// `ref` type prefix → a human noun. Covers the entity types the ledger actually
// carries (from the prod distinct-ref audit); unknown types fall back to 内容.
const REF_TYPE_NOUN: Record<string, string> = {
  galgame: 'Galgame',
  galgame_comment: 'Galgame 评论',
  galgame_quiz: 'Galgame 测验',
  galgame_rating: 'Galgame 评分',
  galgame_resource: 'Galgame 资源',
  galgame_pr: 'Galgame 词条编辑',
  galgame_toolset: 'Galgame 工具集',
  toolset: '工具集',
  toolset_resource: '工具集资源',
  topic: '主题',
  topic_comment: '主题评论',
  topic_reply: '主题回复',
  topic_upvote: '主题',
  comment: '评论',
  resource: '资源'
}

// The entity noun named by a `ref` ("galgame:1207" → "Galgame").
const refTypeNoun = (ref?: string | null): string =>
  REF_TYPE_NOUN[(ref ?? '').split(':')[0] ?? ''] ?? '内容'

// A per-row human label. content_approved / liked name the entity (from `ref`);
// everything else uses the static map, falling back to the raw code so a reason
// added server-side never renders blank.
export const moemoepointReasonLabel = (
  reason: string,
  ref?: string | null
): string => {
  switch (reason) {
    case 'content_approved':
      return `创建了一个新${refTypeNoun(ref)}`
    case 'liked':
      return `${refTypeNoun(ref)}被点赞`
    default:
      return STATIC_REASON_LABEL[reason] ?? reason
  }
}

// source_app is the awarding OAuth client id (opaque) or "oauth" for internal
// grants. The log endpoint enriches rows with a friendly `source_name`; prefer
// it, and map the internal id here as a fallback until it's present.
export const moemoepointSourceLabel = (sourceApp: string): string =>
  sourceApp === 'oauth' ? '账号中心' : sourceApp
