package model

import (
	"time"

	"gorm.io/datatypes"
)

const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

const (
	TriggerSchedule = "schedule"
	TriggerAdmin    = "admin"
)

type JobRun struct {
	ID         uint           `gorm:"primaryKey;autoIncrement" json:"id"`
	JobName    string         `gorm:"size:64;not null;index:idx_job_run_name_started,priority:1" json:"job_name"`
	Trigger    string         `gorm:"size:16;not null" json:"trigger"`
	Status     string         `gorm:"size:16;not null" json:"status"`
	Summary    datatypes.JSON `gorm:"type:jsonb" json:"summary,omitempty"`
	Error      string         `gorm:"type:text;default:''" json:"error,omitempty"`
	StartedAt  time.Time      `gorm:"not null;index:idx_job_run_name_started,priority:2,sort:desc" json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (JobRun) TableName() string { return "job_run" }
