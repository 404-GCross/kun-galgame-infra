package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/devapi"
	"api/pkg/config"
)

// PruneDeveloperUsageOpts configures the developer-usage retention prune.
type PruneDeveloperUsageOpts struct {
	// RetentionDays overrides the default retention window (0 = use the pinned
	// devapi.DeveloperUsageRetentionDays).
	RetentionDays int
	// DryRun counts what would be deleted without deleting.
	DryRun bool
}

// DefaultPruneDeveloperUsageOpts is what the scheduler uses.
func DefaultPruneDeveloperUsageOpts() PruneDeveloperUsageOpts {
	return PruneDeveloperUsageOpts{RetentionDays: devapi.DeveloperUsageRetentionDays}
}

// RunPruneDeveloperUsage deletes developer_api_usage rollup rows older than the
// retention window (day < today − RetentionDays, UTC). developer_api_usage is an
// append-accumulate table that otherwise grows without bound; a daily prune caps
// it at a fixed history.
//
// The table lives in the OAuth core DB (cfg.Database), the same handle the
// developer-platform repository uses. Cross-instance single-flight is provided
// by the runner's per-job-name advisory lock (no manual lock key — same as every
// other job).
func RunPruneDeveloperUsage(ctx context.Context, cfg *config.Config, opts PruneDeveloperUsageOpts) (Summary, error) {
	retentionDays := opts.RetentionDays
	if retentionDays <= 0 {
		retentionDays = devapi.DeveloperUsageRetentionDays
	}

	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	defer db.Close()

	repo := devapi.NewRepository(db.DB())
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format("2006-01-02")

	if opts.DryRun {
		n, err := repo.CountUsageBefore(ctx, cutoff)
		if err != nil {
			return nil, fmt.Errorf("count usage before %s: %w", cutoff, err)
		}
		return Summary{"dry_run": true, "retention_days": retentionDays, "cutoff_day": cutoff, "would_delete": n}, nil
	}

	deleted, err := repo.PruneUsageBefore(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("prune usage before %s: %w", cutoff, err)
	}
	slog.Info("prune-developer-usage: pruned old usage rollup rows",
		"retention_days", retentionDays, "cutoff_day", cutoff, "deleted", deleted)
	return Summary{"retention_days": retentionDays, "cutoff_day": cutoff, "deleted": deleted}, nil
}
