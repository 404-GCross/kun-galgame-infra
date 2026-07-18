// One row of the unified moemoepoint ledger (kun-galgame-infra
// docs/integration/oauth/06-moemoepoint.md §1.2). The self-service / s2s
// "reduced view" omits `note` and `actor_user_id` (admin-only), so those are
// optional here and the same type also serves the admin full view.
export interface MoemoepointLogItem {
  id: number
  delta: number
  reason: string
  source_app: string
  // Friendly name of the awarding site, enriched by the log endpoint from the
  // OAuth client. Optional: absent until the backend populates it.
  source_name?: string
  ref: string
  created_at: string
  actor_user_id?: number
  note?: string
}
