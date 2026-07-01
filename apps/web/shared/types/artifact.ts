// Admin types for the artifact service (kun_artifacts), served by the oauth
// gateway under /api/v1/admin/artifact/*.
//
// The API SHAPES below are now GENERATED from the OpenAPI spec
// (docs/artifact/admin-openapi.yaml, itself exported code-first from the Go Huma
// handlers) — see shared/types/generated/artifact-admin-api.ts and
// `pnpm gen:types:artifact-admin`. Backend field changes surface here at compile
// time. Only the UI-only helpers (status chip map / tabs) are hand-written.
import type { components } from './generated/artifact-admin-api'

type Schemas = components['schemas']

export type ArtifactAdminRow = Schemas['AdminArtifactRow']
export type ArtifactAdminListResponse = Schemas['AdminArtifactList']
export type ArtifactSiteStats = Schemas['AdminArtifactSiteStats']
export type ArtifactAdminStats = Schemas['AdminArtifactStats']
// Union derived from the generated row's status field ('uploading' | 'ready' | 'failed').
export type ArtifactStatus = ArtifactAdminRow['status']

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
