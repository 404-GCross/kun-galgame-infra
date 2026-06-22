// Admin types for the artifact service (kun_artifacts), served by the oauth
// gateway under /admin/artifact/*. Mirrors the Go handler/stats shapes.

export type ArtifactStatus = 'uploading' | 'ready' | 'failed'

export interface ArtifactAdminRow {
  uuid: string
  name: string
  file_key: string
  file_size: number
  mime_type: string
  site_key: string
  status: ArtifactStatus
  public: boolean
  uploader_sub: string
  uploader_client: string
  checksum: string
  created_at: string
}

export interface ArtifactAdminListResponse {
  items: ArtifactAdminRow[]
  total: number
  page: number
  limit: number
}

export interface ArtifactSiteStats {
  count: number
  bytes: number
}

export interface ArtifactAdminStats {
  total_count: number
  total_bytes: number
  uploading: number
  failed: number
  soft_deleted: number
  by_site?: Record<string, ArtifactSiteStats>
}

// Status chip colors, reused by the dashboard + browser pages.
export const ARTIFACT_STATUS_MAP: Record<
  ArtifactStatus,
  { label: string; color: 'success' | 'warning' | 'danger' }
> = {
  ready: { label: '就绪', color: 'success' },
  uploading: { label: '上传中', color: 'warning' },
  failed: { label: '失败', color: 'danger' }
}

export const ARTIFACT_STATUS_TABS = [
  { id: '', label: '全部', icon: 'lucide:list' },
  { id: 'ready', label: '就绪', icon: 'lucide:check' },
  { id: 'uploading', label: '上传中', icon: 'lucide:loader' },
  { id: 'failed', label: '失败', icon: 'lucide:x' }
] as const
