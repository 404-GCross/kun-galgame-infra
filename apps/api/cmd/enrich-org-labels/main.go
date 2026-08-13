package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/orglabels"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	facet := flag.String("facet", "all", "intro | alias | link | all | cien")
	apply := flag.Bool("apply", false, "write (default: dry run — plan counts only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED (also hosts src_vndb / src_bangumi)")
	egDSN := flag.String("eg-dsn", "", "erogamescape DSN (default: catalog DSN with dbname=erogamescape)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "dlsite DSN (cien facet only — hosts cien_profiles)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	if *facet == "cien" {
		st, err := orglabels.RunEnrichCien(context.Background(), orglabels.Opts{
			Apply: *apply, DSN: *dsn, DlsiteDSN: *dlsiteDSN,
		})
		if err != nil {
			slog.Error("enrich-org-labels cien failed", "error", err)
			os.Exit(1)
		}
		slog.Info("enrich-org-labels cien summary", "apply", *apply,
			"creators_200", st.Creators200, "creators_with_desc", st.CreatorsWithDesc,
			"maker_conflicts", st.MakerConflicts, "no_label_match", st.NoLabelMatch,
			"mapped_creators", st.MappedCreators, "mapped_labels", st.MappedLabels,
			"short_skipped", st.ShortSkipped,
			"intro_planned", st.IntroPlanned, "intro_written", st.IntroWritten,
			"intro_skip_dup", st.IntroSkipDup,
			"twitter_planned", st.TwitterPlanned, "twitter_written", st.TwitterWritten,
			"cien_link_planned", st.CienLinkPlanned, "cien_link_written", st.CienLinkWritten,
			"errors", st.Errors)
		if !*apply {
			slog.Info("DRY RUN — nothing written; re-run with --apply")
		}
		return
	}

	st, err := orglabels.RunEnrich(context.Background(), orglabels.Opts{
		Apply: *apply, DSN: *dsn, EGDSN: *egDSN, Facet: *facet,
	})
	if err != nil {
		slog.Error("enrich-org-labels failed", "error", err)
		os.Exit(1)
	}
	slog.Info("enrich-org-labels summary",
		"facet", *facet, "apply", *apply,
		"intro_candidates", st.IntroCandidates, "intro_written", st.IntroWritten,
		"intro_skip_dup", st.IntroSkipDup,
		"alias_candidates", st.AliasCandidates, "alias_written", st.AliasWritten,
		"link_candidates", st.LinkCandidates, "link_written", st.LinkWritten,
		"errors", st.Errors)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
