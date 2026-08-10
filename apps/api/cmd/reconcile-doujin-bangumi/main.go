package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/doujinbangumi"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, tier plan + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate works to process for writing (0 = all); debugging aid")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := doujinbangumi.Run(context.Background(), doujinbangumi.Opts{
		Apply: *apply,
		DSN:   *dsn,
		Limit: *limit,
	})
	if err != nil {
		slog.Error("reconcile-doujin-bangumi", "error", err)
		os.Exit(1)
	}
	slog.Info("reconcile-doujin-bangumi done",
		"apply", *apply,
		"candidate_works", st.CandidateWorks,
		"type4_subjects", st.Type4Subjects,
		"already_anchored", st.AlreadyAnchored,
		"matched", st.Matched,
		"ambiguous_work", st.AmbiguousWork,
		"ambiguous_subject", st.AmbiguousSubject,
		"exact", st.Exact,
		"probable", st.Probable,
		"exact_written", st.ExactWritten,
		"probable_written", st.ProbableWritten,
		"already", st.Already,
	)
}
