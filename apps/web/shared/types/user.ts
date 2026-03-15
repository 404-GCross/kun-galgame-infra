export interface User {
  uuid: string
  name: string
  email: string
  avatar: string
  bio: string
  moemoepoint: number
  status: number
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
