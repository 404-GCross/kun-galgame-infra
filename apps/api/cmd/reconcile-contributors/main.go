// reconcile-contributors backfills galgame_contributor from the editing-engine
// revision log and recovers any credit the best-effort OnMerge hook missed.
//
// galgame_contributor is a MATERIALIZED contributor table, not a pure
// edit_revision projection: 61% of its existing rows predate the revision log
// (original-wiki history the log cannot reconstruct — see the E3b-tail
// reconciliation), so the table stays the source of truth. Going forward the
// engine's galgame.game OnMerge hook keeps it current on the single write path;
// this tool is the offline reconciler that (a) one-time backfills the historical
// gap — revision actors/amenders never written as contributors — and (b)
// recovers the rare best-effort OnMerge miss. Idempotent: it only inserts the
// (galgame, user) pairs the log implies but the table lacks; it never deletes
// (removing a historical contributor is a human action via DELETE
// /galgame/:gid/contributors/:id).
//
// Usage:
//
//	go run ./cmd/reconcile-contributors            # backfill
//	go run ./cmd/reconcile-contributors --dry-run  # report only, no writes
//
// Pools: edit_revision on the catalog DB (KUN_CATALOG_PG_*), galgame_contributor
// on the galgame DB (KUN_GALGAME_PG_*) — the same two-pool posture as
// migrate-catalog (prod: both point at kun_catalog).
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"
)

type pair struct {
	GID int64 `gorm:"column:gid"`
	UID int64 `gorm:"column:uid"`
}

func main() {
	dryRun := flag.Bool("dry-run", false, "report the backfill without writing")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	editDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("connect catalog (edit) database", "error", err)
		os.Exit(1)
	}
	galgameDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame database", "error", err)
		os.Exit(1)
	}

	// Projection implied by the revision log: every positive actor ∪ amender.
	var proj []pair
	if err := editDB.DB().Raw(`
		SELECT entity_id AS gid, actor_uid AS uid FROM edit_revision
		  WHERE entity_type = ? AND actor_uid > 0
		UNION
		SELECT entity_id, amender_uid FROM edit_revision
		  WHERE entity_type = ? AND amender_uid IS NOT NULL AND amender_uid > 0`,
		editspec.TypeGame, editspec.TypeGame).Scan(&proj).Error; err != nil {
		slog.Error("read revision projection", "error", err)
		os.Exit(1)
	}

	// Existing contributor pairs.
	var existingRows []pair
	if err := galgameDB.DB().Raw(
		`SELECT galgame_id AS gid, user_id AS uid FROM galgame_contributor`).
		Scan(&existingRows).Error; err != nil {
		slog.Error("read existing contributors", "error", err)
		os.Exit(1)
	}
	existing := make(map[[2]int64]struct{}, len(existingRows))
	for _, p := range existingRows {
		existing[[2]int64{p.GID, p.UID}] = struct{}{}
	}

	// Missing = projection − existing (deduped; the UNION can still repeat a
	// pair once as actor and once as amender across rows).
	seen := make(map[[2]int64]struct{}, len(proj))
	var missing []model.GalgameContributor
	for _, p := range proj {
		key := [2]int64{p.GID, p.UID}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := existing[key]; ok {
			continue
		}
		missing = append(missing, model.GalgameContributor{GalgameID: int(p.GID), UserID: int(p.UID)})
	}

	slog.Info("contributor reconcile",
		"projection_pairs", len(seen), "existing_pairs", len(existing),
		"to_insert", len(missing), "dry_run", *dryRun)
	if *dryRun || len(missing) == 0 {
		return
	}

	if err := galgameDB.DB().CreateInBatches(missing, 500).Error; err != nil {
		slog.Error("insert missing contributors", "error", err)
		os.Exit(1)
	}
	slog.Info("contributor reconcile complete", "inserted", len(missing))
}
