// backfill-tag-edges materializes galgame_tag_edge from the VNDB tag DAG:
// wiki tags (galgame_tag + English aliases) join src_vndb.tags by primary
// English name, then src_vndb.tags_parents projects parent→child edges onto
// wiki tag ids — compressing over unmapped intermediates and never emitting a
// meta grouping node (searchable=false / applicable=false, e.g. "Type") as a
// parent. The edges power /v1/galgame/tags/multi expand=descendants and the
// tag-detail children block.
//
// Logic lives in internal/jobs/tagedges. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. Reconciles the source="vndb" subset
// only (insert missing + prune stale); user-curated edges are never touched.
// Idempotent — a second --apply writes zero. Re-run after every src_vndb
// dump refresh (cmd/ingest-vndb).
//
// The wiki side is cfg.GalgameDatabase; the src_vndb side is
// cfg.CatalogDatabase (same DB in production, split pools locally) —
// --catalog-dsn overrides it.
//
//	go run ./cmd/backfill-tag-edges              # dry run: counters + samples
//	go run ./cmd/backfill-tag-edges --apply      # write (×2 = idempotent)
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/jobs/tagedges"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	catalogDSN := flag.String("catalog-dsn", "", "src_vndb-side DSN override (default: cfg.CatalogDatabase)")
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

	dsn := *catalogDSN
	if dsn == "" {
		dsn = cfg.CatalogDatabase.DSN()
	}
	vndbDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		slog.Error("catalog (src_vndb) db connect", "error", err)
		os.Exit(1)
	}
	if s, err := vndbDB.DB(); err == nil {
		defer s.Close()
	}

	st, err := tagedges.Run(context.Background(), wikiDB.DB(), vndbDB, tagedges.Opts{Apply: *apply})
	if err != nil {
		slog.Error("backfill-tag-edges", "error", err)
		os.Exit(1)
	}

	slog.Info("backfill-tag-edges summary",
		"apply", *apply,
		"wiki_tags", st.WikiTags,
		"wiki_aliases", st.WikiAliases,
		"vndb_tags", st.VndbTags,
		"vndb_edges", st.VndbEdges,
		"mapped", st.Mapped,
		"ambiguous", st.Ambiguous,
		"planned", st.Planned,
		"compressed", st.Compressed,
		"planned_new", st.PlannedNew,
		"planned_prune", st.PlannedPrune,
		"inserted", st.Inserted,
		"pruned", st.Pruned,
	)
	for _, s := range st.Samples {
		slog.Info("sample edge", "parent", s.Parent, "child", s.Child, "depth", s.Depth)
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
