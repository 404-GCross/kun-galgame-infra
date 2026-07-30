// Package galgameclaim is the resident half of cmd/reconcile-galgame-works: it
// runs that CLI's CLAIM phase — and only that phase — on a nightly schedule, so
// a galgame born today (a sync-vndb draft, a user creation) is registered in the
// catalog registry today instead of waiting for someone to remember the CLI.
//
// Since wave 146 it is no longer the FIRST registrar of a published galgame: the
// write paths claim their own row the moment it is published
// (catalogsync.ClaimOne), because "registered by tomorrow morning" meant up to
// 24h of 404s on a live entry. This job is unchanged and keeps the harder job —
// the reconciliation safety net over the whole population: the drafts the write
// path deliberately skips, anything a write path dropped (its claim is
// best-effort by contract), and every row that predates the lane.
//
// Why only claim. The eg / bangumi phases propose PROBABLE cross-references out
// of staging mirrors (erogamespace, the Bangumi Silver layer) that refresh on
// their own import cadence, so re-running them nightly would be work without new
// input; they stay CLI-driven. Registration is the opposite: its input is the
// wiki's own galgame table, which grows every day, and the gap it leaves is
// silent — an unclaimed galgame has no catalog identity, so it never reaches
// GET /v1/catalog/changes and no downstream mirror can see it exist.
//
// No registration logic lives here. The Reconciler is reused verbatim (options
// in, ClaimStats out), so the nightly run and the CLI cannot drift apart. What
// the claim phase covers is therefore exactly what the CLI's claim phase covers:
// one ClaimWork per galgame, graded exact anchors (wiki vndb_id, audit-ok bid),
// the negative-knowledge gate, and the galgame.catalog_work_id write-back.
//
// Two behaviours inherited from the Reconciler that operators should know:
//
//   - A claim conflict (an anchor already claimed by another product) aborts the
//     whole phase with an error, so one poisoned row parks the backlog until a
//     human resolves it. That is the CLI's contract, kept deliberately: a
//     conflict is a real identity collision, not a row to skip past.
//   - catalog_work.updated_at needs no extra wiring here. The adopt path updates
//     through the model (GORM stamps UpdatedAt) and a fresh mint is a Create, so
//     every newly claimed work enters the changes feed on its own.
package galgameclaim

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/catalogsync"
	"api/pkg/config"

	"gorm.io/gorm"
)

// Opts configures a run. Deliberately just the write gate: the CLI's --limit is
// a debugging aid with no path to reach a scheduled or admin-triggered run.
type Opts struct {
	Apply bool // false = dry run (counts only, writes nothing)
}

// Run registers every unclaimed wiki galgame into the catalog work registry and
// returns a summary. Apply=false reports the plan the same run would execute.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	// The claim phase reads the wiki galgame table and the catalog side (the
	// registry itself, the recorded rejections, and the src_bangumi Silver layer
	// that gates the bid anchor). It never touches erogamespace — that is the eg
	// phase's staging source, so no connection is dialed for it.
	wiki, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer wiki.Close()
	catalog, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db (%s): %w", cfg.CatalogDatabase.DBName, err)
	}
	defer catalog.Close()

	return run(ctx, wiki.DB(), catalog.DB(), opts)
}

// run is Run with the pools already open, so tests can drive it against one
// co-located test database (as production co-locates both families anyway).
func run(ctx context.Context, wiki, catalog *gorm.DB, opts Opts) (map[string]any, error) {
	rc := catalogsync.New(wiki, catalog, nil, catalogsync.Options{
		Phase:  catalogsync.PhaseClaim,
		DryRun: !opts.Apply,
	})
	slog.Info("reconcile-galgame-claims start", "apply", opts.Apply)

	stats, err := rc.Run(ctx, catalogsync.PhaseClaim)
	if err != nil {
		return nil, fmt.Errorf("claim reconcile: %w", err)
	}
	cs := stats.Claim

	slog.Info("reconcile-galgame-claims done",
		"processed", cs.Processed, "claimed_new", cs.ClaimedNew,
		"already_claimed", cs.AlreadyClaimed, "vndb_exact", cs.VNDBExact,
		"bid_exact", cs.BIDExact, "bid_skipped_bad", cs.BIDSkippedBad,
		"skipped_rejected", cs.SkippedRejected,
		"workid_backfilled", cs.WorkIDBackfilled, "workid_covered", cs.WorkIDCovered,
		"claim_state_written", cs.ClaimStateWritten,
		"applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(cs, opts.Apply), nil
}

// summary flattens ClaimStats into the job_run.summary payload. already_claimed
// carries two cases the Reconciler does not separate: works that were already
// ours, and unclaimed registry rows this run adopted through an anchor.
func summary(cs catalogsync.ClaimStats, apply bool) map[string]any {
	return map[string]any{
		"processed":         cs.Processed,
		"claimed_new":       cs.ClaimedNew,
		"already_claimed":   cs.AlreadyClaimed,
		"vndb_exact":        cs.VNDBExact,
		"bid_exact":         cs.BIDExact,
		"bid_skipped_bad":   cs.BIDSkippedBad,
		"skipped_rejected":  cs.SkippedRejected,
		"workid_backfilled": cs.WorkIDBackfilled,
		"workid_covered":    cs.WorkIDCovered,
		// R7: cells whose claim visibility state moved this run. A converged
		// population reports 0 every night; a spike means the wiki republished
		// or withdrew entries.
		"claim_state_written": cs.ClaimStateWritten,
		"applied":             apply,
	}
}
