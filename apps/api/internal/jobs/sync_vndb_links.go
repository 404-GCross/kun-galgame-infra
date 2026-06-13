package jobs

import (
	"context"
	"time"

	"api/internal/jobs/vndblinks"
	"api/pkg/config"
)

// SyncVNDBLinksOpts is re-exported so the cmd shell and registry don't import
// the vndblinks subpackage directly.
type SyncVNDBLinksOpts = vndblinks.Opts

// DefaultSyncVNDBLinksOpts is what the scheduler uses: apply, and only the
// published games still missing VNDB links (new / freshly-claimed) — cheap, a
// few API calls a day. Manual full reconciles go through the CLI.
func DefaultSyncVNDBLinksOpts() SyncVNDBLinksOpts {
	return SyncVNDBLinksOpts{Apply: true, Gap: 2 * time.Second, OnlyMissing: true}
}

// RunSyncVNDBLinks adapts vndblinks.Run to the jobs.Summary contract.
func RunSyncVNDBLinks(ctx context.Context, cfg *config.Config, opts SyncVNDBLinksOpts) (Summary, error) {
	m, err := vndblinks.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
