package jobs

import (
	"context"

	"api/internal/jobs/galgamestats"
	"api/pkg/config"
)

// BuildGalgameStatsOpts is re-exported so the cmd shell and registry don't
// import the galgamestats subpackage directly.
type BuildGalgameStatsOpts = galgamestats.Opts

// DefaultBuildGalgameStatsOpts is what the scheduler uses: recompute + write all
// snapshots (a cheap full-table scan, seconds). Manual previews go through the
// CLI with --dry-run.
func DefaultBuildGalgameStatsOpts() BuildGalgameStatsOpts {
	return BuildGalgameStatsOpts{Apply: true}
}

// RunBuildGalgameStats adapts galgamestats.Run to the jobs.Summary contract.
func RunBuildGalgameStats(ctx context.Context, cfg *config.Config, opts BuildGalgameStatsOpts) (Summary, error) {
	m, err := galgamestats.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
