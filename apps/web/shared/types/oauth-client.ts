export interface OAuthClient {
  id: number
  site_id: number
  client_id: string
  name: string
  redirect_uri: string
  scopes: string[]
  is_active: boolean
  created_at: string
}
