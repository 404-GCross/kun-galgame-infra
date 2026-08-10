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

	_ = godotenv.Load("apps/api/.env")

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
		"char_bgm_touched_works", st.CharBangumi.Touched,
		"char_vndb_candidates", st.CharVNDB.Candidates,
		"char_vndb_en_new", st.CharVNDB.EnNew, "char_vndb_written", st.CharVNDB.EnWritten,
		"char_vndb_spoiler_stripped", st.CharVNDB.SpoilerStripped,
		"char_vndb_touched_works", st.CharVNDB.Touched,
		"char_eg_candidates", st.CharEG.Candidates, "char_eg_no_supply", st.CharEG.NoSupply,
		"char_eg_ja_new", st.CharEG.JaNew, "char_eg_skip_dup_lang", st.CharEG.SkipDupLang,
		"char_eg_written", st.CharEG.JaWritten, "char_eg_touched_works", st.CharEG.Touched,
		"person_bgm_candidates", st.PersonBangumi.Candidates,
		"person_bgm_written", st.PersonBangumi.JaWritten+st.PersonBangumi.ZhWritten,
		"errors", st.CharBangumi.Errors+st.CharVNDB.Errors+st.CharEG.Errors+st.PersonBangumi.Errors,
	)
}
