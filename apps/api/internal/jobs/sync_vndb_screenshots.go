package jobs

import (
	"context"
	"time"

	"api/internal/jobs/vndbscreenshots"
	"api/pkg/config"
)

// SyncVNDBScreenshotsOpts is re-exported so the cmd shell and registry don't
// import the vndbscreenshots subpackage directly.
type SyncVNDBScreenshotsOpts = vndbscreenshots.Opts

// DefaultSyncVNDBScreenshotsOpts is what the scheduler uses: apply, filling the
// published games that still have no VNDB screenshots (new / freshly claimed) —
// a few API calls a day. Targeted top-ups / re-runs go through the CLI.
func DefaultSyncVNDBScreenshotsOpts() SyncVNDBScreenshotsOpts {
	return SyncVNDBScreenshotsOpts{Apply: true, Gap: 2 * time.Second}
}

// RunSyncVNDBScreenshots adapts vndbscreenshots.Run to the jobs.Summary contract.
func RunSyncVNDBScreenshots(ctx context.Context, cfg *config.Config, opts SyncVNDBScreenshotsOpts) (Summary, error) {
	m, err := vndbscreenshots.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
