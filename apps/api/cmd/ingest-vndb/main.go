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
	votesFile := flag.String("votes-file", "", "also stage src_vndb.vn_vote_stats from the separate votes dump (vndb-votes-latest.gz); alone it restages votes only")
	flag.Parse()

	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	ingestDump := *votesFile == "" || explicit["dump-dir"] || explicit["only"]

	_ = godotenv.Load("apps/api/.env")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	dir := ""
	if ingestDump {
		dir, err = resolveDumpDir(*dumpDir)
		if err != nil {
			slog.Error("resolve dump dir", "error", err)
			os.Exit(1)
		}
	}

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := srcvndb.EnsureSchema(db.DB()); err != nil {
		slog.Error("ensure src_vndb schema", "error", err)
		os.Exit(1)
	}

	if ingestDump {
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

	if *votesFile != "" {
		slog.Info("ingesting vndb votes dump", "file", *votesFile)
		vr, err := srcvndb.RunVotes(db.DB(), *votesFile)
		if err != nil {
			slog.Error("votes ingest failed", "error", err)
			os.Exit(1)
		}
		slog.Info("vndb votes ingest completed", "vns", vr.VNs, "votes", vr.Votes, "unparsed", vr.Skipped)
	}
}

func resolveDumpDir(dir string) (string, error) {
	for _, candidate := range []string{dir, filepath.Join("..", "..", dir)} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("dump directory not found: %s (tried as-is and ../../)", dir)
}
