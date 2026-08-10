export interface OAuthClient {
  id: string
  site_id?: number
  name: string
  redirect_uris: string[]
  grants: string[]
  allowed_scopes?: string[]
  is_public?: boolean
  // page silently skips the user consent UI for already-logged-in users
  auto_consent?: boolean
  refresh_token_ttl_seconds?: number
  listed?: boolean
  logo_url?: string
  tagline?: string
  display_order?: number
  created_at: string
  storage?: OAuthClientStorageConfig
}

export interface OAuthClientStorageConfig {
  artifact_enabled: boolean
  artifact_site_key: string
  artifact_cdn_base: string
  artifact_allowed_mime: string[]
  artifact_max_file_size: number
  artifact_quota_daily: number
  artifact_quota_bytes_daily: number
  image_enabled: boolean
  image_site_key: string
  image_cdn_base: string
  image_allowed_presets: string[]
  image_max_file_size: number
  image_quota_daily: number
  image_quota_bytes_daily: number
}

export const DEFAULT_REFRESH_TOKEN_TTL_SECONDS = 60 * 60 * 24 * 90

export interface OAuthClientCreated extends OAuthClient {
  secret: string
}

export interface EcosystemApp {
  name: string
  site_domain: string
  logo_url?: string
  tagline?: string
  auto_consent: boolean
}

export const ALL_GRANTS = ['authorization_code', 'refresh_token'] as const

export const KNOWN_SCOPES = [
  'openid',
  'profile',
  'email',
  'image:upload',
  'artifact:upload',
] as const

export const REN_ONLY_SCOPES: readonly string[] = ['image:upload', 'artifact:upload']
