// probable-promote executes a user-ratified batch-upgrade policy: promote the
// probable work-refs of the named matched_by rules to exact (step 26,
// B-track). It is a thin, re-runnable driver around the admin bucket's
// ConfirmRef — no new write rule, no schema — so the reconciler keeps writing
// probable (R8 doctrine unchanged) and this tool is re-run periodically as new
// probable volume accrues ("trust but keep sampling").
//
//	go run ./cmd/probable-promote --actor <id> --rule rule:eg-vndb-rosetta --rule rule:title-year-strict         # dry
//	go run ./cmd/probable-promote --actor <id> --rule rule:eg-vndb-rosetta --rule rule:title-year-strict --run   # write
//
// --actor is the policy executor; it lands in verified_by, meaning "policy
// approval of the sampled-precision decision", NOT a per-row human review.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

// ruleFlags collects repeated --rule values.
type ruleFlags []string

func (r *ruleFlags) String() string { return strings.Join(*r, ",") }
func (r *ruleFlags) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func main() {
	var rules ruleFlags
	flag.Var(&rules, "rule", "a matched_by rule string to promote (repeatable)")
	actor := flag.Int64("actor", 0, "policy-executor user id recorded as verified_by (required)")
	apply := flag.Bool("run", false, "write changes (default: dry-run preview)")
	limit := flag.Int("limit", 0, "cap the refs processed (0 = all); debugging aid")
	flag.Parse()

	if *actor <= 0 {
		fmt.Fprintln(os.Stderr, "--actor <id> is required (recorded as verified_by)")
		os.Exit(2)
	}
	if len(rules) == 0 {
		fmt.Fprintln(os.Stderr, "at least one --rule <matched_by> is required")
		os.Exit(2)
	}

	_ = godotenv.Load("apps/api/.env")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	if _, err := runPromote(context.Background(), catalogDB.DB(), os.Stdout, rules, *actor, *apply, *limit); err != nil {
		slog.Error("probable-promote failed", "error", err)
		os.Exit(1)
	}
	if *apply {
		fmt.Fprintln(os.Stdout, "note: run reconcile-galgame-works, enrich-bangumi, then reindex-catalog to propagate the new exact anchors.")
	}
}
