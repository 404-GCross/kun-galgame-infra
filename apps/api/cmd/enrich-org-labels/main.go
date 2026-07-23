// enrich-org-labels lands the E2b org/label enrichment wave (refs/proj/83):
// intros, aliases and external links onto the labels anchored by E2a
// (reconcile-org-labels). All facets are fill-missing and idempotent.
//
//   - intro: VNDB producer descriptions (markup-stripped, 3-way lang) + Bangumi
//     company/group summaries → catalog_label_intro.
//   - alias: VNDB aliases (spelling variant) + EG furigana (search hint) →
//     catalog_label_alias.
//   - link:  EG official site / twitter / cien + Bangumi infobox site / twitter
//     → catalog_external_ref(link_kind=related).
//
// Run E2a first — enrichment follows the anchors. The catalog DSN (--dsn,
// REQUIRED) hosts src_vndb / src_bangumi; erogamespace is a separate database
// (--eg-dsn). Dry-run is the default.
//
//	go run ./cmd/enrich-org-labels --facet all --dsn "$DSN"           # dry run
//	go run ./cmd/enrich-org-labels --facet all --dsn "$DSN" --apply   # write (×2 = idempotent)
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
	facet := flag.String("facet", "all", "intro | alias | link | all")
	apply := flag.Bool("apply", false, "write (default: dry run — plan counts only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED (also hosts src_vndb / src_bangumi)")
	egDSN := flag.String("eg-dsn", "", "erogamespace DSN (default: catalog DSN with dbname=erogamespace)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
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
