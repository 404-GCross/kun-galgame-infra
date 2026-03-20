export interface OAuthClient {
  id: string
  site_id?: number
  name: string
  redirect_uris: string[]
  grants: string[]
  created_at: string
}

export interface OAuthClientCreated extends OAuthClient {
  secret: string
}
