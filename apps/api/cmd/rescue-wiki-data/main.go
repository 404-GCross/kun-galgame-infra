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
//	j  official map      galgame_official.id → label      → catalog_external_ref (exact)
//	k  engine map        galgame_engine.id → engine       → catalog_external_ref (exact)
//	l  tag map           galgame_tag.id → canonical tag   → catalog_external_ref (exact)
//
//	m  cover bytes       ALL galgame_cover                → catalog_work_cover
//	n  screenshot bytes  ALL galgame_screenshot           → catalog_work_screenshot
//	o  image ownership   catalog-referenced hashes        → image_site_usage (site=catalog)
//
//	p  titles mirror     galgame names + galgame_alias    → catalog_work_title
//	q  intros mirror     galgame.intro_{ja,en}            → catalog_work_intro
//	r  tags mirror       galgame_tag_relation ⋈ galgame_tag → catalog_work_tag (+ catalog_tag.sexual)
//	s  vndb ratings      galgame_vndb_meta                → catalog_work_rating
//	t  meta adoption     galgame_{bangumi,dlsite,eg}_meta  → catalog_work_rating / _popularity
//
// Steps j..l are the A2-0 registrar rescue (refs/proj/127): the three taxonomy
// id maps that today only exist as a joinable wiki table.
//
// Steps m..o are the W2 image-byte retirement (refs/proj/128): the wiki media
// tables are the only reference source for 174,843 image-service hashes, so the
// rows are projected whole and the catalog site is given ownership of the hashes
// — after which the existing catalog reference ping keeps the bytes alive. Step o
// is the ONLY step that writes to the images database, and it only ever INSERTs.
//
// Steps p..t are the W1-pre read-time-bridge nativization (refs/proj/140): the
// catalog read face bridged those five facets out of the wiki tables for every
// CLAIMED work, and these steps materialize the bridges verbatim so the face can
// be flipped to one native lane and the tables dropped. They are MIRRORS, so they
// insert, update AND delete inside a declared ownership scope; p,q,r,s are
// idempotent followers, t is one-shot (never re-run it — see the package doc).
//
//	go run ./cmd/rescue-wiki-data --step p,q,r,s          # the daily follow-up
//	go run ./cmd/rescue-wiki-data --step t --apply        # exactly once
//
// Steps a..o are fill-missing (ON CONFLICT DO NOTHING) and p..t converge, so in
// both cases a second pass changes nothing — that is the acceptance criterion.
// Dry run is the default; for a mirror step it is an exact forecast (the
// insert/update/delete ledger reads the same in dry and apply).
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
// --step also takes a comma-separated selection, which is how a later wave runs
// its own steps without replaying the converged ones:
//
//	go run ./cmd/rescue-wiki-data --step m,n,o            # the W2 wave alone
//
// Databases come from the environment (KUN_CATALOG_PG_DATABASE /
// KUN_GALGAME_PG_DATABASE, plus KUN_IMAGES_PG_DATABASE for step o), never a flag
// — rehearsal runs simply point those at the rehearsal databases.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"api/internal/infrastructure/database"
	"api/internal/jobs/wikirescue"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	step := flag.String("step", "all", "which steps to run: a..o, a comma-separated selection, or all")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	artifacts := flag.String("artifacts", "", "directory for parked-row JSON (empty = do not write)")
	flag.Parse()

	selected, err := wikirescue.ParseSteps(*step)
	if err != nil {
		slog.Error("parse steps", "error", err)
		os.Exit(1)
	}

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

	// The images pool is opened only when a selected step needs it: the wiki
	// rescue steps must stay runnable when kun_images is out of reach.
	if slices.Contains(selected, wikirescue.StepImageUsage) {
		imagesDB, err := database.NewPostgresDB(cfg.ImagesDatabase)
		if err != nil {
			slog.Error("images db connect", "error", err, "dbname", cfg.ImagesDatabase.DBName)
			os.Exit(1)
		}
		defer imagesDB.Close()
		runner.WithImages(imagesDB.DB())
		slog.Info("images pool attached", "dbname", cfg.ImagesDatabase.DBName)
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
	// The mirror ledger (steps p..t) is a second table: its insert/update/delete
	// split is the whole acceptance signal, and it reads the same in dry and apply.
	mirror := make([]wikirescue.Stats, 0, len(stats))
	for _, s := range stats {
		if s.Insert+s.Update+s.Delete+s.Already+s.Skipped+s.Vocab > 0 {
			mirror = append(mirror, s)
		}
	}
	if len(mirror) > 0 {
		fmt.Printf("\n%-5s %9s %9s %9s %9s %9s %9s\n",
			"step", "insert", "update", "delete", "already", "skipped", "vocab")
		for _, s := range mirror {
			fmt.Printf("%-5s %9d %9d %9d %9d %9d %9d\n",
				s.Step, s.Insert, s.Update, s.Delete, s.Already, s.Skipped, s.Vocab)
		}
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
