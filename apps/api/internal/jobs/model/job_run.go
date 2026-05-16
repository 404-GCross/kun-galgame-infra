// Package model holds the job_run audit/observability row for the job
// registry. It is intentionally dependency-light (time + datatypes only)
// so cmd/migrate can AutoMigrate it without pulling the scheduler/runner.
package model

import (
	"time"

	"gorm.io/datatypes"
)

// Run status values.
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusSkipped = "skipped" // e.g. another instance held the advisory lock
)

// Trigger values.
const (
	TriggerSchedule = "schedule"
	TriggerAdmin    = "admin"
)

// JobRun is one execution record of a registered job. Lives in the OAuth
// core DB (cfg.Database) for central visibility, regardless of which DB
// the job itself touches.
type JobRun struct {
	ID      uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	JobName string `gorm:"size:64;not null;index:idx_job_run_name_started,priority:1" json:"job_name"`
	Trigger string `gorm:"size:16;not null" json:"trigger"` // schedule | admin
	Status  string `gorm:"size:16;not null" json:"status"`  // running | success | failed | skipped
	// Summary is the job's structured result on success (or {reason:...}
	// for skipped). Free-form per job — not schema-validated.
	Summary   datatypes.JSON `gorm:"type:jsonb" json:"summary,omitempty"`
	Error     string         `gorm:"type:text;default:''" json:"error,omitempty"`
	StartedAt time.Time      `gorm:"not null;index:idx_job_run_name_started,priority:2,sort:desc" json:"started_at"`
	// FinishedAt is nil while running. A long-nil running row after process
	// restart is reaped to failed:stale by the runner's startup self-check.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TableName pins the table name (avoid gorm's pluralization).
func (JobRun) TableName() string { return "job_run" }
