// import-getchu-characters projects the Getchu crawler's character rosters onto
// catalog characters: the profile prose and the typed attributes
// (refs/proj/167 §9).
//
// Both DSNs are REQUIRED. The staging side is the kun-getchu-api database (in
// production it sits beside dlsite and erogamespace in the same postgres);
// the catalog side is the live registry.
//
//	go run ./cmd/import-getchu-characters --dsn "$CATALOG" --getchu-dsn "$GETCHU"
//	go run ./cmd/import-getchu-characters --dsn "$CATALOG" --getchu-dsn "$GETCHU" --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/getchuchars"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	getchuDSN := flag.String("getchu-dsn", "", "getchu staging DSN — REQUIRED")
	limit := flag.Int("limit", 0, "max matched characters to process (0 = all)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := getchuchars.Run(context.Background(), getchuchars.Opts{
		DSN: *dsn, GetchuDSN: *getchuDSN, Apply: *apply, Limit: *limit,
	})
	if st != nil {
		slog.Info("import-getchu-characters done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("import-getchu-characters", "error", err)
		os.Exit(1)
	}
}
