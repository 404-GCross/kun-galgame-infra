// Types for the user-submission review queue. Mirrors the wiki API shapes
// from docs/integration/galgame_wiki/08-messages.md.

export type ReviewMessageType =
  | 'submitted'
  | 'claimed'
  | 'edited_pending'
  | 'approved'
  | 'declined'
  | 'banned'
  | 'unbanned'

export interface ReviewMessageGalgameBrief {
  id: number
  name_en_us: string
  name_ja_jp: string
  name_zh_cn: string
  name_zh_tw: string
  banner: string
  banner_image_hash?: string | null
  effective_banner_hash?: string | null
  status: number
  user_id: number
}

export interface ReviewMessageActor {
  id: number
  name: string
  avatar: string
}

export interface ReviewMessage {
  id: number
  type: ReviewMessageType
  galgame_id: number
  galgame?: ReviewMessageGalgameBrief | null
  actor_user_id?: number | null
  // Admin queue endpoint enriches actor with the user brief. Other endpoints
  // (mine / feed) omit it.
  actor?: ReviewMessageActor | null
  target_user_id?: number | null
  payload?: Record<string, unknown> | null
  created_at: string
}

export interface ReviewQueueResponse {
  items: ReviewMessage[]
  total: number
}

// Body for PUT /admin/galgame/:gid/status. Targets 0 (publish/unban),
// 1 (ban), 4 (decline). Reason is optional except UX-wise for decline.
export interface StatusUpdateRequest {
  status: 0 | 1 | 4
  reason?: string
}
