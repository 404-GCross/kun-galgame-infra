// enrich-bangumi enriches galgames from the Bangumi Silver layer
// (src_bangumi.subject in kun_catalog): fills empty Chinese name/intro,
// appends missing aliases, and refreshes the galgame_bangumi_meta narrow
// table. Conservative by design — see the bangumienrich package doc.
//
// The candidate set is the UNION of the curated galgame.bid column and the
// catalog Bangumi exact anchors reached via galgame.catalog_work_id (step 43).
// galgame.bid is never written back; a bid/anchor disagreement is exported to
// the --conflicts-out TSV for human review, never auto-resolved.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. After an
// applied run, rebuild the search index (invariant 21):
//
//	go run ./cmd/enrich-bangumi                 # dry run, full statistics
//	go run ./cmd/enrich-bangumi --apply         # write
//	go run ./cmd/reindex-search --index=galgames
//
// --catalog-dsn overrides the catalog connection (both the anchor face and the
// src_bangumi Silver source). Empty = the KUN_CATALOG_* config (production
// live kun_catalog). Locally, ALWAYS point it at kun_catalog_rehearsal — the
// live kun_catalog serves letmoe testing and must not be touched.
package main

import (
	"encoding/csv"
	"flag"
	"log/slog"
	"os"
	"strconv"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/bangumienrich"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max candidate galgames to process (0 = all)")
	catalogDSN := flag.String("catalog-dsn", "", "catalog DSN override (anchor face + Silver source); empty = KUN_CATALOG_* config (production live kun_catalog). Locally point at kun_catalog_rehearsal — never the live kun_catalog.")
	conflictsOut := flag.String("conflicts-out", "bangumi-bid-conflicts.tsv", "path to write the bid-vs-anchor conflict worklist (TSV; written whenever conflicts exist, dry or apply)")
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

	// Catalog side: an explicit --catalog-dsn (the rehearsal copy locally) wins
	// over the config default (production live kun_catalog).
	catalogGorm, closeCatalog, err := openCatalog(cfg, *catalogDSN)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer closeCatalog()

	stats, conflicts, err := bangumienrich.Run(wikiDB.DB(), catalogGorm, bangumienrich.Options{
		DryRun: !*apply,
		Limit:  *limit,
	})
	if err != nil {
		slog.Error("enrich failed", "error", err)
		os.Exit(1)
	}

	slog.Info("bangumi enrichment summary",
		"candidates_bid", stats.CandidatesBID,
		"candidates_anchor_only", stats.CandidatesAnchorOnly,
		"conflicts", stats.Conflicts,
		"matched", stats.Matched,
		"wrong_type", stats.WrongType,
		"missing_in_dump", stats.MissingInDump,
		"name_filled", stats.NameFilled,
		"intro_filled", stats.IntroFilled,
		"aliases_appended", stats.AliasesAppended,
		"meta_upserted", stats.MetaUpserted,
		"unchanged", stats.Unchanged,
	)

	if len(conflicts) > 0 {
		if err := writeConflicts(*conflictsOut, conflicts); err != nil {
			slog.Error("write conflicts worklist", "path", *conflictsOut, "error", err)
			os.Exit(1)
		}
		slog.Warn("bid-vs-anchor conflicts exported (zero automatic action taken)",
			"count", len(conflicts), "path", *conflictsOut)
	}

	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	} else {
		slog.Info("applied — run `go run ./cmd/reindex-search --index=galgames` to refresh search")
	}
}

// openCatalog returns the catalog *gorm.DB plus a close func. With dsn set it
// opens that DSN directly (rehearsal override); otherwise it uses the
// KUN_CATALOG_* config (production live kun_catalog).
func openCatalog(cfg *config.Config, dsn string) (*gorm.DB, func(), error) {
	if dsn != "" {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			return nil, nil, err
		}
		slog.Info("catalog DSN override in effect (rehearsal) — live kun_catalog untouched")
		return db, func() {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}, nil
	}
	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, nil, err
	}
	return catalogDB.DB(), func() { catalogDB.Close() }, nil
}

// writeConflicts exports the bid-vs-anchor conflict worklist as a TSV
// (galgame_id, wiki_bid, anchor_bid, name) for L1 human adjudication.
func writeConflicts(path string, conflicts []bangumienrich.Conflict) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Comma = '\t'
	if err := w.Write([]string{"galgame_id", "wiki_bid", "anchor_bid", "name"}); err != nil {
		return err
	}
	for _, c := range conflicts {
		if err := w.Write([]string{
			strconv.Itoa(c.GalgameID),
			strconv.Itoa(c.WikiBID),
			strconv.Itoa(c.AnchorBID),
			c.Name,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
