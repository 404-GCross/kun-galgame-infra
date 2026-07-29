// backfill-entity-intros projects character and person descriptions from the
// staging mirrors into catalog_character_intro / catalog_person_intro (field PR
// C1, refs/proj/65 + refs/proj/120). Four lanes: character × bangumi summaries
// (kana-detected ja / zh-Hans), character × vndb descriptions (en, spoiler
// spans removed entirely, light markup unwrapped), character × erogamespace
// appearance write-ups (ja, spoiler rows excluded, longest text per character),
// person × bangumi summaries (person anchors are empty today — the lane
// auto-expands when identity resolution lands them). src_bangumi / src_vndb are
// schemas INSIDE the catalog DB, so ONE --dsn covers those three lanes;
// erogamespace is a separate DATABASE and enters via --eg-dsn.
//
// Fill-missing-language discipline (step 57): a text is written in its
// language ONLY when the entity has no intro row in that language yet (any
// source). No upsert — intros are non-volatile (contrast: the step-62
// popularity counters change on every mirror refresh and need
// ON CONFLICT DO UPDATE); a dump refresh is handled by re-running
// fill-missing. Logic lives in internal/jobs/entityintros. Idempotent: a
// second --apply writes zero.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted — the rehearsal copy locally
// (kun_catalog_rehearsal), the live catalog only in the acceptance run.
// --eg-dsn defaults to the catalog server with dbname=erogamespace (the
// cmd/enrich-eg-scores form); it is only consulted when the char-eg lane runs.
//
//	# dry-run: per-lane counters + samples
//	go run ./cmd/backfill-entity-intros \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply one lane, chunked
//	go run ./cmd/backfill-entity-intros --apply --only char-vndb --limit 1000 \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/entityintros"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate entities per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate entities per lane (for chunking)")
	only := flag.String("only", "", "restrict to one lane: char-bgm | char-vndb | char-eg | person-bgm (default: all)")
	egDSN := flag.String("eg-dsn", "", "erogamespace staging DSN (default: erogamespace db on the catalog server)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives logging and the char-eg default DSN; the catalog DB itself
	// is reached exclusively via --dsn.
	cfg, cfgErr := config.Load()
	if cfgErr == nil {
		logger.Init(cfg.Server.Env)
	}
	eg := *egDSN
	if eg == "" && cfgErr == nil {
		egCfg := cfg.CatalogDatabase
		egCfg.DBName = "erogamespace"
		eg = egCfg.DSN()
	}

	st, err := entityintros.Run(context.Background(), entityintros.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
		Only:   *only,
		EGDSN:  eg,
	})
	if err != nil {
		slog.Error("backfill-entity-intros", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-entity-intros done", "apply", *apply,
		"char_bgm_candidates", st.CharBangumi.Candidates,
		"char_bgm_ja_new", st.CharBangumi.JaNew, "char_bgm_zh_new", st.CharBangumi.ZhNew,
		"char_bgm_written", st.CharBangumi.JaWritten+st.CharBangumi.ZhWritten,
		"char_vndb_candidates", st.CharVNDB.Candidates,
		"char_vndb_en_new", st.CharVNDB.EnNew, "char_vndb_written", st.CharVNDB.EnWritten,
		"char_vndb_spoiler_stripped", st.CharVNDB.SpoilerStripped,
		"char_eg_candidates", st.CharEG.Candidates, "char_eg_no_supply", st.CharEG.NoSupply,
		"char_eg_ja_new", st.CharEG.JaNew, "char_eg_skip_dup_lang", st.CharEG.SkipDupLang,
		"char_eg_written", st.CharEG.JaWritten, "char_eg_touched_works", st.CharEG.Touched,
		"person_bgm_candidates", st.PersonBangumi.Candidates,
		"person_bgm_written", st.PersonBangumi.JaWritten+st.PersonBangumi.ZhWritten,
		"errors", st.CharBangumi.Errors+st.CharVNDB.Errors+st.CharEG.Errors+st.PersonBangumi.Errors,
	)
}
