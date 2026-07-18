// Background-job registry admin surface: GET /admin/jobs (list + latest run),
// POST /admin/jobs/:name/run (manual trigger), GET /admin/jobs/:name/runs
// (history). Mirrors apps/api/internal/jobs/model/job_run.go + the /admin/jobs
// jobView.

export type JobStatus = 'running' | 'success' | 'failed' | 'skipped'
export type JobTrigger = 'schedule' | 'admin'

export interface JobRun {
  id: number
  job_name: string
  trigger: JobTrigger
  status: JobStatus
  // The job's structured result on success (or { reason } for skipped);
  // free-form per job, not schema-validated.
  summary?: Record<string, unknown> | null
  error?: string
  started_at: string
  // Absent while running.
  finished_at?: string | null
  created_at: string
}

export interface JobInfo {
  name: string
  desc: string
  // "HH:MM" daily schedule — present only for time-scheduled jobs.
  daily_at?: string
  // auto=false → manual-trigger only.
  auto: boolean
  latest_run: JobRun | null
}
