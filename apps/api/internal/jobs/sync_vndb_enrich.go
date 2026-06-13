package jobs

import (
	"context"
	"time"

	"api/internal/jobs/vndbenrich"
	"api/pkg/config"
)

// SyncVNDBEnrichOpts is re-exported so the cmd shell and registry don't import
// the vndbenrich subpackage directly.
type SyncVNDBEnrichOpts = vndbenrich.Opts

// DefaultSyncVNDBEnrichOpts is what the scheduler uses: apply, and only the
// published games still missing VNDB links (new / freshly-claimed) — cheap, a
// few API calls a day. Manual full reconciles go through the CLI.
func DefaultSyncVNDBEnrichOpts() SyncVNDBEnrichOpts {
	return SyncVNDBEnrichOpts{Apply: true, Gap: 2 * time.Second, OnlyMissing: true}
}

// RunSyncVNDBEnrich adapts vndbenrich.Run to the jobs.Summary contract.
func RunSyncVNDBEnrich(ctx context.Context, cfg *config.Config, opts SyncVNDBEnrichOpts) (Summary, error) {
	m, err := vndbenrich.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
