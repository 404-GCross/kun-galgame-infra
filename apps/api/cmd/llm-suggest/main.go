package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/llmsuggest"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

const defaultGoldSet = "internal/platform/catalog/llmsuggest/testdata/goldset.jsonl"

func main() {
	task := flag.String("task", "", "goldset | residue | sanity")
	apply := flag.Bool("apply", false, "write suggestions (default: dry run — sample + print)")
	limit := flag.Int("limit", 0, "cap items processed (0 = all); dry-run sample size")
	conc := flag.Int("concurrency", 4, "parallel LLM calls (<=4)")
	llmBase := flag.String("llm-base", "http://127.0.0.1:8002/v1", "vLLM OpenAI base URL")
	model := flag.String("model", "qwen3-14b", "served model id")
	goldPath := flag.String("goldset", defaultGoldSet, "gold set JSONL path")
	egDSN := flag.String("eg-dsn", "", "erogamescape staging DSN (build-goldset; default: erogamescape on the catalog server)")
	buildGold := flag.Bool("build-goldset", false, "regenerate the gold set JSONL from local dumps, then exit")
	calibrate := flag.Bool("calibrate", false, "print calibration metrics from persisted goldset verdicts, then exit")
	batch := flag.Bool("batch", false, "goldset: judge in batches (throughput comparison; prompt_version v1-batch)")
	flag.Parse()

	promptVersion := llmsuggest.PromptNamePairV1
	if *batch {
		promptVersion = llmsuggest.PromptNamePairV1B
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
	if err := llmsuggest.EnsureSchema(catalogDB.DB()); err != nil {
		slog.Error("ensure src_llm schema", "error", err)
		os.Exit(1)
	}

	if *conc > 4 {
		*conc = 4
	}
	ctx := context.Background()

	if *buildGold {
		egDB := openEG(cfg, *egDSN)
		stats, err := llmsuggest.BuildGoldSet(catalogDB.DB(), egDB, *goldPath)
		if err != nil {
			slog.Error("build gold set", "error", err)
			os.Exit(1)
		}
		fmt.Printf("gold set written: %s\n", *goldPath)
		fmt.Printf("total=%d positive=%d negative=%d\n", stats.Total, stats.Positive, stats.Negative)
		fmt.Printf("layers: %v\n", stats.Layers)
		fmt.Printf("paren filtered: role=%d circle=%d persona=%d\n",
			stats.ParenFilteredRole, stats.ParenFilteredCircle, stats.ParenFilteredPersona)
		return
	}

	if *calibrate {
		metrics, err := llmsuggest.Calibrate(catalogDB.DB(), *model, promptVersion)
		if err != nil {
			slog.Error("calibrate", "error", err)
			os.Exit(1)
		}
		printCalibration(metrics)
		return
	}

	if *task == "" {
		slog.Error("no --task given (goldset | residue | bid-audit), or use --build-goldset / --calibrate")
		os.Exit(2)
	}

	client := llmsuggest.NewClient(*llmBase, *model)
	models, err := client.Ping(ctx)
	if err != nil {
		fmt.Printf("BLOCKED: local vLLM unreachable at %s (%v).\n"+
			"Start kungal-llm-infra and retry — this is a designed precondition, not a failure.\n", *llmBase, err)
		os.Exit(3)
	}
	slog.Info("vLLM reachable", "base", *llmBase, "models", models)

	opts := llmsuggest.Options{Model: *model, Concurrency: *conc, Limit: *limit, DryRun: !*apply, GoldSetPath: *goldPath, Batch: *batch}

	failures := 0
	switch *task {
	case "goldset":
		judged, errs, err := llmsuggest.RunGoldset(ctx, catalogDB.DB(), client, opts)
		fail(err)
		failures = errs
		if *apply {
			slog.Info("goldset done", "batch", *batch, "judged", judged, "errors", errs)
			metrics, err := llmsuggest.Calibrate(catalogDB.DB(), *model, promptVersion)
			fail(err)
			printCalibration(metrics)
		}
	case "residue":
		done, errs, err := llmsuggest.RunResidue(ctx, catalogDB.DB(), client, opts)
		fail(err)
		failures = errs
		if *apply {
			slog.Info("residue done", "extracted", done, "errors", errs)
		}
	case "sanity":
		mean, sampled, err := llmsuggest.RunSanity(ctx, catalogDB.DB(), client, *limit)
		fail(err)
		fmt.Printf("sanity: extraction↔parser key overlap = %.3f over %d parse-OK infoboxes\n", mean, sampled)
		return
	default:
		slog.Error("unknown task", "task", *task)
		os.Exit(2)
	}
	if !*apply {
		fmt.Println("[dry run] nothing written — re-run with --apply")
	}
	if failures > 0 {
		os.Exit(1)
	}
}

func openEG(cfg *config.Config, dsn string) *gorm.DB {
	if dsn == "" {
		egCfg := cfg.CatalogDatabase
		egCfg.DBName = "erogamescape"
		dsn = egCfg.DSN()
	}
	db, err := database.OpenJob(dsn)
	if err != nil {
		slog.Error("erogamescape connect", "error", err)
		os.Exit(1)
	}
	return db
}

func printCalibration(metrics []llmsuggest.LayerMetrics) {
	fmt.Printf("\n%-26s %5s %4s %4s %4s %4s %6s %6s %6s %8s\n",
		"layer", "n", "TP", "FP", "FN", "TN", "unsure", "prec", "recall", "acc(-uns)")
	for _, m := range metrics {
		fmt.Printf("%-26s %5d %4d %4d %4d %4d %6d %6.3f %6.3f %8.3f\n",
			m.Layer, m.N, m.TP, m.FP, m.FN, m.TN, m.Unsure, m.Precision, m.Recall, m.AccuracyExclUnsure)
	}
}

func fail(err error) {
	if err != nil {
		slog.Error("task failed", "error", err)
		os.Exit(1)
	}
}
