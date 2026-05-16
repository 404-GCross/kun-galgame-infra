package jobs

import (
	"context"

	"api/internal/jobs/vndbsync"
	"api/pkg/config"
)

// SyncVNDBOpts is re-exported so the cmd shell and registry don't import
// the vndbsync subpackage directly.
type SyncVNDBOpts = vndbsync.Opts

// DefaultSyncVNDBOpts is what the scheduler uses: incremental, tagMap
// path from env / repo-root default.
func DefaultSyncVNDBOpts() SyncVNDBOpts {
	return SyncVNDBOpts{Full: false, TagMapPath: vndbsync.DefaultTagMapPath()}
}

// RunSyncVNDB adapts vndbsync.Run to the jobs.Summary contract.
func RunSyncVNDB(ctx context.Context, cfg *config.Config, opts SyncVNDBOpts) (Summary, error) {
	m, err := vndbsync.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
