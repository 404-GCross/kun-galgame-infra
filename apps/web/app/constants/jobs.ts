import type { JobStatus, JobTrigger } from '~~/shared/types/jobs'

export const JOB_STATUS_MAP: Record<
  JobStatus,
  { label: string; color: 'info' | 'success' | 'danger' | 'default' }
> = {
  running: { label: '运行中', color: 'info' },
  success: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'danger' },
  skipped: { label: '跳过', color: 'default' }
}

export const jobStatusMeta = (s: string) =>
  JOB_STATUS_MAP[s as JobStatus] ?? { label: s, color: 'default' as const }

export const JOB_TRIGGER_LABEL: Record<JobTrigger, string> = {
  schedule: '定时',
  admin: '手动'
}

export const jobTriggerLabel = (t: string) =>
  JOB_TRIGGER_LABEL[t as JobTrigger] ?? t
