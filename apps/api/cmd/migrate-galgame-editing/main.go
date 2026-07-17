// cmd/migrate-galgame-editing — the one-shot galgame → editing-engine legacy
// transform (E2a, 03 号裁定 3, D2 彻底做法).
//
// It reads galgame_revision + galgame_pr from the galgame pool
// (KUN_GALGAME_PG_*) and upserts the unified log rows into edit_revision /
// edit_proposal on the catalog pool (KUN_CATALOG_PG_*) — the same two-pool
// posture as cmd/migrate-catalog, so production (both = kun_catalog) and dev
// (split databases) are both correct by construction.
//
// Semantics:
//   - Idempotent: every row upserts keyed on its SOURCE PK (legacy_id /
//     legacy_pr_id) — re-running repairs, never duplicates.
//   - Delta: a later run picks up only source rows written since the previous
//     pass (the freeze-window catch-up that makes the E2b production cutover
//     freeze-free).
//   - Merged PRs (status=1) become merged edit_proposal rows linked to their
//     revision; pending/declined PRs are DROPPED (user ruling D2).
//   - Requires cmd/migrate-catalog to have run first (legacy bookkeeping
//     columns; see catalog/migrate.EditLegacyColumns).
//
// PREREQUISITE — archive the source tables once before the first run (pure
// insurance; dropped pending/declined PRs live only in this dump afterwards):
//
//	pg_dump -h <host> -U <user> -t galgame_pr -t galgame_revision <galgame_db> \
//	    -f galgame-editing-archive-$(date +%F).sql
//
// Production runs this binary MANUALLY in the E2b window (04 号 runbook) —
// it is NOT part of the per-deploy migrate-catalog.
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/editbridge"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	verifyOnly := flag.Bool("verify-only", false, "run the four reconciliation gates without transforming")
	sample := flag.Int("sample", 200, "gate-3 row-fidelity sample size (<=0 checks every row)")
	batch := flag.Int("batch", 500, "revision scan batch size")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	slog.Info("connecting to galgame content database", "dbname", cfg.GalgameDatabase.DBName)
	galgameDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to galgame content database", "error", err)
		os.Exit(1)
	}
	defer galgameDB.Close()

	slog.Info("connecting to catalog database", "dbname", cfg.CatalogDatabase.DBName)
	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("failed to connect to catalog database", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	if !*verifyOnly {
		slog.Info("transforming galgame revisions + merged PRs into the engine log...")
		summary, err := editbridge.Transform(galgameDB.DB(), catalogDB.DB(), editbridge.TransformOpts{Batch: *batch})
		if err != nil {
			slog.Error("transform failed", "error", err)
			os.Exit(1)
		}
		slog.Info("transform complete", "summary", summary.String())
	}

	slog.Info("running reconciliation gates...", "sample", *sample)
	report, err := editbridge.Verify(galgameDB.DB(), catalogDB.DB(), *sample)
	if err != nil {
		slog.Error("verification gate failed", "error", err)
		os.Exit(1)
	}
	slog.Info("verification gates passed",
		"action_buckets", report.ActionBuckets,
		"gids_checked", report.GidsChecked,
		"rows_sampled", report.SampleChecked,
		"proposals_linked", report.ProposalsLinked,
	)
}
