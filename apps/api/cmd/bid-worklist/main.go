// bid-worklist turns the step-12 bid-identity audit backlog (126 different + 61
// unsure + 2 boundary) into a human-clearable loop:
//
//	--export                  # write the review worklist TSV (fill the decision column)
//	--apply --file wl.tsv      # dry-run the full-chain receipts (per-row actions printed)
//	--apply --file wl.tsv --run  # actually execute
//	--export-smears            # read-only: games flagged smear, for alias cleanup later
//
// It never decides for the human — the LLM verdict is only a reference column;
// the decision column the human fills is the sole driver. Rollback is safe:
// an enrichment-filled field is reverted ONLY when its current value still
// equals what enrichment wrote (a later hand-edit is never clobbered).
//
// The verdict table (src_llm) lives in kun_catalog; the wiki rollback touches
// kun_galgame_wiki. This is a composition-root cmd, so it may span both.
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/llmsuggest"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	export := flag.Bool("export", false, "write the human-review worklist TSV")
	exportSmears := flag.Bool("export-smears", false, "write the smear cleanup worklist (read-only)")
	apply := flag.Bool("apply", false, "apply a filled-in worklist receipt")
	run := flag.Bool("run", false, "with --apply, actually write (default: dry)")
	all := flag.Bool("all", false, "with --export, include already-resolved rows")
	includeCleared := flag.Bool("include-cleared-suspect", false, "with --export, also include LLM-cleared suspects (layer=suspect,verdict=same) — the deeper audit pass that catches LLM false-negatives like the canary")
	out := flag.String("out", "", "output path for --export (default: stdout)")
	file := flag.String("file", "", "with --apply, the filled receipt TSV")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	catalogConn, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogConn.Close()
	wikiConn, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiConn.Close()
	catalogDB, wikiDB := catalogConn.DB(), wikiConn.DB()

	// Tool-owned schema: the verdict table's resolution columns. NOT a product
	// migration — llmsuggest owns src_llm; EnsureSchema is idempotent.
	if err := llmsuggest.EnsureSchema(catalogDB); err != nil {
		slog.Error("ensure src_llm schema", "error", err)
		os.Exit(1)
	}

	switch {
	case *export:
		if err := runExport(catalogDB, *out, *all, *includeCleared); err != nil {
			slog.Error("export", "error", err)
			os.Exit(1)
		}
	case *exportSmears:
		if err := runExportSmears(catalogDB, *out); err != nil {
			slog.Error("export-smears", "error", err)
			os.Exit(1)
		}
	case *apply:
		if *file == "" {
			slog.Error("--apply requires --file")
			os.Exit(1)
		}
		st, err := runApply(catalogDB, wikiDB, *file, *run)
		if err != nil {
			slog.Error("apply", "error", err)
			os.Exit(1)
		}
		st.log(*run)
	default:
		slog.Error("one of --export / --export-smears / --apply is required")
		os.Exit(1)
	}
}
