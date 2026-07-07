// probable-audit builds the evidence base for a batch-upgrade POLICY decision
// on the ~28k probable work-refs the reconciler proposed (doc 17 §8 P2b): it
// mechanically cross-checks every probable ref, draws a deterministic stratified
// sample for human review, and reports the sample's measured precision plus the
// mechanical consequences of each candidate policy. It NEVER decides the policy
// and NEVER touches a non-sampled row.
//
// Four modes (dry-run is the default for the writing one):
//
//	go run ./cmd/probable-audit --scan                        # full cross-check matrix + contradiction list
//	go run ./cmd/probable-audit --export --out samples.tsv    # deterministic stratified sample worklist
//	go run ./cmd/probable-audit --apply --file receipt.tsv    # dry: preview the ok/wrong receipt actions
//	go run ./cmd/probable-audit --apply --file receipt.tsv --run   # write (reuses admin confirm/reject)
//	go run ./cmd/probable-audit --report --file receipt.tsv   # per-subgroup precision + policy estimates
//
// Like the reconciler, the EG staging DSN enters ONLY through --eg-dsn (never
// config) and defaults to an `erogamespace` db on the catalog server. The
// receipt's ok/wrong verdicts reuse the admin review-queue service methods
// verbatim (ConfirmRef / RejectRef) so a receipt is exactly equivalent to a
// human clicking the probable-ref bucket.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	scan := flag.Bool("scan", false, "full mechanical cross-check: rule × corroboration × year matrix + contradiction list")
	export := flag.Bool("export", false, "write the deterministic stratified sample worklist")
	apply := flag.Bool("apply", false, "process a filled receipt (dry-run unless --run)")
	report := flag.Bool("report", false, "summarize a receipt into a per-subgroup precision table + policy estimates")
	run := flag.Bool("run", false, "with --apply: actually write (default: dry-run preview)")
	file := flag.String("file", "", "receipt TSV path (--apply / --report)")
	out := flag.String("out", "probable-audit-samples.tsv", "output path (--export); a trailing / or a directory writes the default filename inside it")
	actor := flag.Int64("actor", 0, "operator id recorded as verified_by / rejected_by on receipt actions")
	egDSN := flag.String("eg-dsn", "", "erogamespace staging DSN (default: erogamespace db on the catalog server)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	// EG is needed by the modes that classify the full population (scan/export
	// build rosetta context; report's policy estimates need the strata sizes).
	// --apply acts only on receipt refKeys and never touches EG.
	var egDB *gorm.DB
	if *scan || *export || *report {
		dsn := *egDSN
		if dsn == "" {
			egCfg := cfg.CatalogDatabase
			egCfg.DBName = "erogamespace"
			dsn = egCfg.DSN()
		}
		egDB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			slog.Error("erogamespace db connect", "error", err)
			os.Exit(1)
		}
		if sqlDB, err := egDB.DB(); err == nil {
			defer sqlDB.Close()
		}
	}

	a := &auditor{wiki: wikiDB.DB(), catalog: catalogDB.DB(), eg: egDB}
	ctx := context.Background()

	switch {
	case *scan:
		mustRun(a.runScan(os.Stdout))
	case *export:
		mustRun(a.runExport(*out))
	case *apply:
		if *file == "" {
			fail("--apply requires --file <receipt.tsv>")
		}
		mustRun(a.runApply(ctx, *file, *run, *actor))
	case *report:
		if *file == "" {
			fail("--report requires --file <receipt.tsv>")
		}
		mustRun(a.runReport(os.Stdout, *file))
	default:
		fail("pick one mode: --scan | --export | --apply | --report")
	}
}

func mustRun(err error) {
	if err != nil {
		slog.Error("probable-audit failed", "error", err)
		os.Exit(1)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}
