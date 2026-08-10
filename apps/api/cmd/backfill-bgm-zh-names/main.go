package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/bgmzhnames"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	lane := flag.String("lane", string(bgmzhnames.LaneCharacter), "entity family: character | person | label")
	limit := flag.Int("limit", 0, "max anchored entities to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many anchored entities (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := bgmzhnames.Run(context.Background(), bgmzhnames.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Lane:   bgmzhnames.Lane(*lane),
		Limit:  *limit,
		Offset: *offset,
	})
	if err != nil {
		slog.Error("backfill-bgm-zh-names", "error", err)
		os.Exit(1)
	}
	for _, s := range st.Samples {
		slog.Info("bgm-zh-names sample", "entity", s.EntityID, "owner", s.OwnerID, "bgm", s.ExternalID,
			"name", s.Name, "primary", s.Primary)
	}
	slog.Info("backfill-bgm-zh-names done", "lane", *lane, "apply", *apply,
		"anchored", st.Anchored, "skipped_no_owner", st.SkippedNoOwner,
		"skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"skipped_non_chinese", st.SkippedNonChinese, "skipped_same_as_owner", st.SkippedSameAsOwner,
		"candidates", st.Candidates, "names", st.Names,
		"would_insert", st.WouldInsert, "skipped_dup", st.SkippedDup,
		"inserted", st.Inserted, "primary_set", st.PrimarySet, "conflict", st.Conflict,
		"touched_works", st.Touched, "errors", st.Errors)
}
