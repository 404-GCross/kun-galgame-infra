package jobs

import (
	"context"

	"api/internal/jobs/galgameclaim"
	"api/pkg/config"
)

// ReconcileGalgameClaimsOpts is re-exported so the registry (and any future cmd
// shell) doesn't import the galgameclaim subpackage directly.
type ReconcileGalgameClaimsOpts = galgameclaim.Opts

// DefaultReconcileGalgameClaimsOpts is what the scheduler uses: apply. The phase
// is idempotent — a converged night claims nothing and writes nothing — so there
// is no reason to schedule it as a preview.
func DefaultReconcileGalgameClaimsOpts() ReconcileGalgameClaimsOpts {
	return ReconcileGalgameClaimsOpts{Apply: true}
}

// RunReconcileGalgameClaims adapts galgameclaim.Run to the jobs.Summary contract.
func RunReconcileGalgameClaims(ctx context.Context, cfg *config.Config, opts ReconcileGalgameClaimsOpts) (Summary, error) {
	m, err := galgameclaim.Run(ctx, cfg, opts)
	if m == nil {
		return nil, err
	}
	return Summary(m), err
}
