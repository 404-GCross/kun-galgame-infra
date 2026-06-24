export interface User {
  uuid: string
  name: string
  email: string
  avatar: string                  // legacy URL string (kungal/moyu old WebP)
  avatar_image_hash?: string | null  // image_service hash, set on new uploads
  bio: string
  moemoepoint: number
  status: number
  is_anonymized?: boolean // PII irreversibly scrubbed (terminal); shows 已注销
  roles: string[]
  created_at: string
}

export interface LoginResponse {
  user: User
  access_token: string
}

export interface RefreshResponse {
  access_token: string
}

// One account in the browser's session "bag" (multi-account support).
// Returned by GET /auth/sessions. `sub` is the user uuid; `active:true`
// marks the currently-active account. See docs/integration/oauth/09.
export interface BagSession {
  sub: string
  name: string
  email: string
  avatar: string
  avatar_image_hash?: string | null
  active: boolean
  last_used_at: string
}
