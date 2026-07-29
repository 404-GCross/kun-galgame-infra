// rescue-wiki-data lands the data-layer-retirement W0 wave (refs/proj/121,
// refs/plans/10-data-layer-retirement/00-charter.md): the one-shot rescue of
// every wiki-family row with no upstream to regenerate it from, run before the
// galgame_* table family is dropped.
//
// Steps, in charter order (--step):
//
//	a  engine facet      galgame_engine + engine_relation → catalog_engine / catalog_work_engine
//	b  screenshots       non-VNDB galgame_screenshot      → catalog_work_screenshot
//	c  links             non-VNDB galgame_link            → catalog_external_ref (related)
//	d  tag intros        galgame_tag.description          → catalog_tag_intro
//	e  label intros      galgame_official.description     → catalog_label_intro
//	f  series intros     galgame_series.description       → catalog_series_intro
//	g  official tail     unmapped galgame_official        → catalog_label (+ brand edges)
//	h  user originals    vndb_id='' works                 → catalog_work + catalog_work_title
//	i  gid map           galgame.id → work                → catalog_external_ref (exact)
//
// Every step is fill-missing (ON CONFLICT DO NOTHING), so a second pass writes
// nothing — that is the acceptance criterion. Dry run is the default.
//
// The schema this wave needs (catalog_engine, catalog_work_engine,
// catalog_tag_intro, catalog_series_intro, the `web` source seed) ships in
// cmd/migrate-catalog — run that FIRST.
//
//	go run ./cmd/migrate-catalog
//	go run ./cmd/rescue-wiki-data --step all              # plan
//	go run ./cmd/rescue-wiki-data --step all --apply      # write
//	go run ./cmd/rescue-wiki-data --step all --apply      # ×2 = idempotent, writes 0
//
// Databases come from the environment (KUN_CATALOG_PG_DATABASE /
// KUN_GALGAME_PG_DATABASE), never a flag — rehearsal runs simply point those
// at the rehearsal database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/jobs/wikirescue"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	step := flag.String("step", "all", "which step to run: a..i, or all")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	artifacts := flag.String("artifacts", "", "directory for parked-row JSON (empty = do not write)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	galgameDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("galgame db connect", "error", err, "dbname", cfg.GalgameDatabase.DBName)
		os.Exit(1)
	}
	defer galgameDB.Close()

	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err, "dbname", cfg.CatalogDatabase.DBName)
		os.Exit(1)
	}
	defer catalogDB.Close()

	runner, err := wikirescue.New(galgameDB.DB(), catalogDB.DB(), wikirescue.Opts{
		Apply: *apply, Step: *step, ArtifactDir: *artifacts,
	})
	if err != nil {
		slog.Error("wire runner", "error", err)
		os.Exit(1)
	}

	stats, err := runner.Run(context.Background())
	report(stats, *apply, cfg.CatalogDatabase.DBName)
	if err != nil {
		slog.Error("rescue-wiki-data failed", "error", err)
		os.Exit(1)
	}
}

func report(stats []wikirescue.Stats, apply bool, dbname string) {
	fmt.Printf("\n=== rescue-wiki-data %s (catalog=%s) ===\n", mode(apply), dbname)
	fmt.Printf("%-5s %9s %9s %8s %9s %9s %9s %9s\n",
		"step", "source", "anchored", "parked", "planned", "written", "touched", "created")
	for _, s := range stats {
		fmt.Printf("%-5s %9d %9d %8d %9d %9d %9d %9d\n",
			s.Step, s.Source, s.Anchored, s.Parked, s.Planned, s.Written, s.Touched, s.Created)
	}
	for _, s := range stats {
		if s.Note != "" {
			fmt.Printf("  [%s] %s\n", s.Step, s.Note)
		}
	}
	if !apply {
		fmt.Println("\nDRY RUN — nothing written; re-run with --apply")
	}
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
