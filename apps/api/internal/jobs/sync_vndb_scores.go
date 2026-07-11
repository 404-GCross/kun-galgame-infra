package jobs

import (
	"context"
	"time"

	"api/internal/jobs/vndbscores"
	"api/pkg/config"
)

// SyncVNDBScoresOpts is re-exported so the cmd shell and registry don't import
// the vndbscores subpackage directly.
type SyncVNDBScoresOpts = vndbscores.Opts

// DefaultSyncVNDBScoresOpts is what the scheduler uses: apply, all vndb-anchored
// games (a few hundred cheap /vn calls a day). Manual sampled trials go through
// the CLI with --limit.
func DefaultSyncVNDBScoresOpts() SyncVNDBScoresOpts {
	return SyncVNDBScoresOpts{Apply: true, Gap: 2 * time.Second}
}

// RunSyncVNDBScores adapts vndbscores.Run to the jobs.Summary contract.
func RunSyncVNDBScores(ctx context.Context, cfg *config.Config, opts SyncVNDBScoresOpts) (Summary, error) {
	m, err := vndbscores.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
