// backfill-bgm-zh-names projects the Simplified-Chinese character names carried
// by the Bangumi infobox into catalog_character_alias (lang=zh-Hans,
// kind=translation), over the EXISTING character anchors (refs/proj/151). The
// supply is the `简体中文名` field plus the Chinese-declaring items of `别名`;
// src_bangumi is a schema INSIDE the catalog DB, so ONE --dsn covers the run.
// Logic lives in internal/jobs/bgmzhnames.
//
// Idempotent by construction: the alias unique key is (character_id, name,
// lang) and the write is ON CONFLICT DO NOTHING, so a second --apply writes
// zero rows and touches zero works. is_primary_for_locale is claimed only where
// the character has no zh-Hans primary yet — an existing one is never flipped.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted — the rehearsal copy locally
// (kun_catalog_rehearsal), the live catalog only in the acceptance run.
//
//	# dry-run: counters + samples
//	go run ./cmd/backfill-bgm-zh-names \
//	    --dsn "host=localhost port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply, chunked
//	go run ./cmd/backfill-bgm-zh-names --apply --limit 5000 --offset 0 \
//	    --dsn "host=localhost port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
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
	limit := flag.Int("limit", 0, "max anchored characters to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many anchored characters (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives logging only; the catalog DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := bgmzhnames.Run(context.Background(), bgmzhnames.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
	})
	if err != nil {
		slog.Error("backfill-bgm-zh-names", "error", err)
		os.Exit(1)
	}
	for _, s := range st.Samples {
		slog.Info("bgm-zh-names sample", "character", s.CharacterID, "bgm", s.ExternalID,
			"name", s.Name, "primary", s.Primary)
	}
	slog.Info("backfill-bgm-zh-names done", "apply", *apply,
		"anchored", st.Anchored, "skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"skipped_non_chinese", st.SkippedNonChinese,
		"candidates", st.Candidates, "names", st.Names,
		"would_insert", st.WouldInsert, "skipped_dup", st.SkippedDup,
		"inserted", st.Inserted, "primary_set", st.PrimarySet, "conflict", st.Conflict,
		"touched_works", st.Touched, "errors", st.Errors)
}
