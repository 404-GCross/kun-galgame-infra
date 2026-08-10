package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	jobmodel "api/internal/jobs/model"
	"api/pkg/config"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Runner struct {
	cfg *config.Config
	db  *gorm.DB
}

func NewRunner(cfg *config.Config, db *gorm.DB) *Runner {
	return &Runner{cfg: cfg, db: db}
}

func advisoryKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}

func (r *Runner) ReapStale(ctx context.Context, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	res := r.db.WithContext(ctx).Model(&jobmodel.JobRun{}).
		Where("status = ? AND started_at < ?", jobmodel.StatusRunning, cutoff).
		Updates(map[string]any{
			"status":      jobmodel.StatusFailed,
			"error":       "stale: process restarted or run exceeded max age",
			"finished_at": time.Now(),
		})
	if res.Error != nil {
		slog.Error("jobs: reap stale runs", "err", res.Error)
		return
	}
	if res.RowsAffected > 0 {
		slog.Warn("jobs: reaped stale running rows", "count", res.RowsAffected)
	}
}

func (r *Runner) Run(ctx context.Context, job Job, trigger string) (runID uint, status string) {
	sqlDB, err := r.db.DB()
	if err != nil {
		slog.Error("jobs: get sql.DB", "job", job.Name, "err", err)
		return 0, jobmodel.StatusFailed
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		slog.Error("jobs: acquire conn", "job", job.Name, "err", err)
		return 0, jobmodel.StatusFailed
	}
	defer conn.Close()

	key := advisoryKey(job.Name)
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
		slog.Error("jobs: try advisory lock", "job", job.Name, "err", err)
		return 0, jobmodel.StatusFailed
	}
	if !locked {
		slog.Info("jobs: skipped, lock held", "job", job.Name, "trigger", trigger)
		id := r.record(ctx, job.Name, trigger, jobmodel.StatusSkipped,
			Summary{"reason": "locked"}, "")
		return id, jobmodel.StatusSkipped
	}
	defer func() {
		if _, uerr := conn.ExecContext(context.Background(),
			"SELECT pg_advisory_unlock($1)", key); uerr != nil {
			slog.Error("jobs: advisory unlock", "job", job.Name, "err", uerr)
		}
	}()

	start := time.Now()
	runID = r.record(ctx, job.Name, trigger, jobmodel.StatusRunning, nil, "")
	slog.Info("jobs: run start", "job", job.Name, "trigger", trigger, "run_id", runID)

	summary, runErr := safeRun(ctx, job, r.cfg)
	finished := time.Now()

	upd := map[string]any{"finished_at": finished}
	if runErr != nil {
		status = jobmodel.StatusFailed
		upd["status"] = status
		upd["error"] = runErr.Error()
		slog.Error("jobs: run failed", "job", job.Name, "run_id", runID,
			"dur", finished.Sub(start).String(), "err", runErr)
	} else {
		status = jobmodel.StatusSuccess
		upd["status"] = status
		if js, jerr := json.Marshal(summary); jerr == nil {
			upd["summary"] = datatypes.JSON(js)
		}
		slog.Info("jobs: run ok", "job", job.Name, "run_id", runID,
			"dur", finished.Sub(start).String(), "summary", summary)
	}
	if runID != 0 {
		if err := r.db.WithContext(ctx).Model(&jobmodel.JobRun{}).
			Where("id = ?", runID).Updates(upd).Error; err != nil {
			slog.Error("jobs: update job_run", "job", job.Name, "run_id", runID, "err", err)
		}
	}
	return runID, status
}

func (r *Runner) LatestRun(ctx context.Context, name string) (*jobmodel.JobRun, error) {
	var row jobmodel.JobRun
	err := r.db.WithContext(ctx).
		Where("job_name = ?", name).
		Order("started_at DESC").
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, err
	}
	if row.ID == 0 {
		return nil, nil
	}
	return &row, nil
}

func (r *Runner) ListRuns(ctx context.Context, name string, limit int) ([]jobmodel.JobRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	var rows []jobmodel.JobRun
	err := r.db.WithContext(ctx).
		Where("job_name = ?", name).
		Order("started_at DESC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *Runner) RunAsync(job Job, trigger string) {
	go r.Run(context.Background(), job, trigger)
}

func safeRun(ctx context.Context, job Job, cfg *config.Config) (s Summary, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return job.Run(ctx, cfg)
}

func (r *Runner) record(ctx context.Context, name, trigger, status string, summary Summary, errMsg string) uint {
	row := &jobmodel.JobRun{
		JobName:   name,
		Trigger:   trigger,
		Status:    status,
		Error:     errMsg,
		StartedAt: time.Now(),
	}
	if status != jobmodel.StatusRunning {
		now := time.Now()
		row.FinishedAt = &now
	}
	if summary != nil {
		if js, err := json.Marshal(summary); err == nil {
			row.Summary = datatypes.JSON(js)
		}
	}
	if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
		slog.Error("jobs: insert job_run", "job", name, "err", err)
		return 0
	}
	return row.ID
}
