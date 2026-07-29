// reconcile-galgame-works registers every wiki galgame into the catalog work
// registry with graded external anchors, then proposes two families of
// probable cross-references. Three phases run in order (claim → eg → bangumi):
//
//   - claim: ClaimWork one catalog_work per galgame; exact refs for vndb_id
//     and audit-ok bids (doc 17 R8 tier-0-treated first-party anchors);
//   - eg: erogamespace vndb rosetta → probable refs (community tier-1);
//   - bangumi: strict title+year+bidirectional-unique match → probable refs.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. The EG
// phase reads a STAGING database whose DSN enters only through --eg-dsn (never
// config); it defaults to an `erogamespace` db on the catalog server.
//
//	go run ./cmd/reconcile-galgame-works                 # dry-run, all phases
//	go run ./cmd/reconcile-galgame-works --apply         # write
//	go run ./cmd/reconcile-galgame-works --phase eg --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/catalogsync"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	phase := flag.String("phase", catalogsync.PhaseAll, "claim | eg | bangumi | all")
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	egDSN := flag.String("eg-dsn", "", "erogamespace staging DSN (default: erogamespace db on the catalog server)")
	limit := flag.Int("limit", 0, "max wiki galgames to process (0 = all); debugging aid")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	// The EG connection is only needed when the EG phase runs.
	var egDB *gorm.DB
	if *phase == catalogsync.PhaseEG || *phase == catalogsync.PhaseAll {
		dsn := *egDSN
		if dsn == "" {
			egCfg := cfg.CatalogDatabase
			egCfg.DBName = "erogamespace"
			dsn = egCfg.DSN()
		}
		conn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		})
		if err != nil {
			slog.Error("erogamespace db connect", "error", err)
			os.Exit(1)
		}
		if sqlDB, err := conn.DB(); err == nil {
			defer sqlDB.Close()
		}
		egDB = conn
	}

	rc := catalogsync.New(wikiDB.DB(), catalogDB.DB(), egDB, catalogsync.Options{
		Phase:  *phase,
		DryRun: !*apply,
		Limit:  *limit,
	})
	stats, err := rc.Run(context.Background(), *phase)
	if err != nil {
		slog.Error("reconcile failed", "error", err)
		os.Exit(1)
	}

	if *phase == catalogsync.PhaseClaim || *phase == catalogsync.PhaseAll {
		slog.Info("claim phase",
			"processed", stats.Claim.Processed,
			"claimed_new", stats.Claim.ClaimedNew,
			"already_claimed", stats.Claim.AlreadyClaimed,
			"vndb_exact", stats.Claim.VNDBExact,
			"bid_exact", stats.Claim.BIDExact,
			"bid_skipped_bad", stats.Claim.BIDSkippedBad,
			"skipped_rejected", stats.Claim.SkippedRejected,
			"claim_state_written", stats.Claim.ClaimStateWritten,
		)
	}
	if *phase == catalogsync.PhaseEG || *phase == catalogsync.PhaseAll {
		slog.Info("eg rosetta phase",
			"matched", stats.EG.Matched,
			"ambiguous_skipped", stats.EG.AmbiguousSkipped,
			"refs_written", stats.EG.RefsWritten,
			"already", stats.EG.Already,
			"unclaimed", stats.EG.Unclaimed,
			"skipped_rejected", stats.EG.SkippedRejected,
		)
	}
	if *phase == catalogsync.PhaseBangumi || *phase == catalogsync.PhaseAll {
		slog.Info("bangumi title-match phase",
			"candidates_lhs", stats.Bangumi.CandidatesLHS,
			"matched_unique", stats.Bangumi.MatchedUnique,
			"rejected_ambiguous", stats.Bangumi.RejectedAmbiguous,
			"refs_written", stats.Bangumi.RefsWritten,
			"already", stats.Bangumi.Already,
			"skipped_rejected", stats.Bangumi.SkippedRejected,
		)
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
