package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/charattrs"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate characters per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate characters per lane (for chunking)")
	only := flag.String("only", "", "restrict to one lane: vndb | bgm (default: both)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := charattrs.Run(context.Background(), charattrs.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
		Only:   *only,
	})
	if err != nil {
		slog.Error("backfill-character-attrs", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-character-attrs done", "apply", *apply,
		"vndb_candidates", st.VNDB.Candidates, "vndb_rows_updated", st.VNDB.RowsUpdated,
		"vndb_gender", st.VNDB.GenderWritten,
		"bgm_candidates", st.Bangumi.Candidates, "bgm_rows_updated", st.Bangumi.RowsUpdated,
		"bgm_gender", st.Bangumi.GenderWritten, "bgm_extra_rows", st.Bangumi.ExtraRows,
		"out_of_range", st.VNDB.OutOfRange+st.Bangumi.OutOfRange,
		"errors", st.VNDB.Errors+st.Bangumi.Errors,
	)
}
