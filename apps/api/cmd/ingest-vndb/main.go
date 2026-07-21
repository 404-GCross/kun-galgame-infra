// Command ingest-vndb loads a VNDB database dump (vndb.org/d14, PostgreSQL
// COPY format) into the src_vndb schema of kun_catalog (Silver),
// deterministically and re-runnably (whole-table replacement per file). Staged
// (see srcvndb.Files for the full set): the work tables (vn, vn_relations),
// the character tables (chars — full columns since step 72 — chars_names,
// chars_vns, images with character "ch" portraits only, chars_traits), the
// staff family (staff, staff_alias, vn_staff, vn_seiyuu), the vocabularies
// (traits, tags + their parent edges, tags_vn votes), producers, and the
// releases family (releases + vn/producers/platforms/titles child tables).
// See the srcvndb package doc.
//
//	# extract the dump's db/ directory somewhere first:
//	#   zstd -dc vndb-db-latest.tar.zst | tar -x
//	go run ./cmd/ingest-vndb --dump-dir /path/to/db          # from apps/api
//	go run ./cmd/ingest-vndb --dump-dir /path/to/db --only vn
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/srcvndb"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	dumpDir := flag.String("dump-dir", "refs/vndb-dump/db", "directory containing the extracted VNDB dump db/ files (data files + *.header)")
	only := flag.String("only", "", "ingest a single file (any name in srcvndb.Files, e.g. vn | chars | staff | releases | tags_vn)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	dir, err := resolveDumpDir(*dumpDir)
	if err != nil {
		slog.Error("resolve dump dir", "error", err)
		os.Exit(1)
	}

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// The Silver schema is tool-owned (fully rebuildable staging) — deliberately
	// NOT part of cmd/migrate-catalog's Gold migration order.
	if err := srcvndb.EnsureSchema(db.DB()); err != nil {
		slog.Error("ensure src_vndb schema", "error", err)
		os.Exit(1)
	}

	slog.Info("ingesting vndb dump", "dir", dir, "only", *only)
	report, err := srcvndb.Run(db.DB(), dir, *only)
	if err != nil {
		slog.Error("ingest failed", "error", err)
		os.Exit(1)
	}
	for _, name := range srcvndb.Files {
		if fr, ok := report.PerFile[name]; ok {
			slog.Info("ingested", "file", name, "rows", fr.Rows, "skipped", fr.Skipped)
		}
	}
	slog.Info("vndb ingest completed", "total", report.Duration.String())
}

// resolveDumpDir accepts the directory as given, or relative to the repo root
// when running from apps/api (and vice versa).
func resolveDumpDir(dir string) (string, error) {
	for _, candidate := range []string{dir, filepath.Join("..", "..", dir)} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("dump directory not found: %s (tried as-is and ../../)", dir)
}
