// reconcile-org-labels lands the E2a org/label anchoring wave (refs/proj/83):
// it anchors catalog_label to the VNDB producer / Bangumi company+group /
// erogamespace brand spaces by structural work co-occurrence (name equality as
// the tie-breaker / weak signal), and mints a new label + work_label edges for
// any source org with works but no matching label. VNDB persons (type=in) may
// anchor an existing label but never mint one.
//
// The catalog DSN (--dsn, REQUIRED) also hosts the src_vndb / src_bangumi
// staging schemas; erogamespace is a separate database (--eg-dsn, default: the
// catalog DSN with dbname=erogamespace). Dry-run is the default.
//
//	go run ./cmd/reconcile-org-labels --source all --dsn "$DSN"           # dry run
//	go run ./cmd/reconcile-org-labels --source all --dsn "$DSN" --apply   # write (×2 = idempotent)
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
	source := flag.String("source", "all", "vndb | bangumi | eg | all")
	apply := flag.Bool("apply", false, "write (default: dry run — plan counts only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED (also hosts src_vndb / src_bangumi)")
	egDSN := flag.String("eg-dsn", "", "erogamespace DSN (default: catalog DSN with dbname=erogamespace)")
	limit := flag.Int("limit", 0, "cap orgs processed per source (0 = all); debugging aid")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := orglabels.RunAnchor(context.Background(), orglabels.Opts{
		Apply: *apply, DSN: *dsn, EGDSN: *egDSN, Source: *source, Limit: *limit,
	})
	if err != nil {
		slog.Error("reconcile-org-labels failed", "error", err)
		os.Exit(1)
	}
	slog.Info("reconcile-org-labels summary",
		"source", *source, "apply", *apply,
		"orgs", st.Orgs, "already", st.Already,
		"anchors_exact", st.AnchorsExact, "anchors_probable", st.AnchorsProbable,
		"new_labels", st.NewLabels, "new_edges", st.NewEdges,
		"conflict", st.Conflict, "skip_no_match", st.SkipNoMatch,
		"skip_ambiguous", st.SkipAmbiguous, "skip_ungradeable", st.SkipUngradeable,
		"vndb_in_anchored", st.VNDBInAnchored, "errors", st.Errors)
	// The spine is reported separately, never folded into the totals above: its
	// warrant for creating a label is participation in the corporate graph, not
	// work attribution, and collapsing the two would hide which rule acted.
	slog.Info("reconcile-org-labels spine summary",
		"considered", st.Spine.Considered, "minted", st.Spine.Minted,
		"anchored", st.Spine.Anchored, "candidates", st.Spine.Candidates,
		"candidate_rows", st.Spine.CandidateRows,
		"skip_claimed", st.Spine.SkipClaimed, "skip_edgeless", st.Spine.SkipEdgeless,
		"skip_alias_only", st.Spine.SkipAliasOnly, "errors", st.Spine.Errors)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
