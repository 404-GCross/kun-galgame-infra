package jobs

import (
	"context"
	"time"

	"api/internal/jobs/vndbcovers"
	"api/pkg/config"
)

// SyncVNDBCoversOpts is re-exported so the cmd shell and registry don't import
// the vndbcovers subpackage directly.
type SyncVNDBCoversOpts = vndbcovers.Opts

// DefaultSyncVNDBCoversOpts is what the scheduler uses: apply, API mode (no
// dump), filling the published games that still have no cover (new / freshly
// claimed VNDB drafts — Claim never creates a cover) — a few API calls a day.
// The one-time bulk dump backfill goes through the CLI with --vndb-image-dir.
func DefaultSyncVNDBCoversOpts() SyncVNDBCoversOpts {
	return SyncVNDBCoversOpts{Apply: true, Gap: 2 * time.Second}
}

// RunSyncVNDBCovers adapts vndbcovers.Run to the jobs.Summary contract.
func RunSyncVNDBCovers(ctx context.Context, cfg *config.Config, opts SyncVNDBCoversOpts) (Summary, error) {
	m, err := vndbcovers.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
