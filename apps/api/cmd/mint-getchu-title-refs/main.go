// mint-getchu-title-refs anchors Getchu products VNDB never linked, by
// agreement between the two catalogues (refs/proj/167 §13).
//
// A match on title + brand + exact release date that resolves to exactly one
// work and one release, CONFIRMED by an overlap between the two character
// rosters, is written link_kind=exact. A match that cannot be confirmed is
// inference only and is left alone unless --probable-too is passed, in which
// case it is written link_kind=probable — inert until an adjudication wave.
//
// Both DSNs are REQUIRED.
//
//	go run ./cmd/mint-getchu-title-refs --dsn "$CATALOG" --getchu-dsn "$GETCHU"
//	go run ./cmd/mint-getchu-title-refs --dsn "$CATALOG" --getchu-dsn "$GETCHU" --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/getchutitlerefs"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write the refs (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	getchuDSN := flag.String("getchu-dsn", "", "getchu staging DSN — REQUIRED")
	probableToo := flag.Bool("probable-too", false, "also write unconfirmed matches at link_kind=probable")
	limit := flag.Int("limit", 0, "max matches to process (0 = all)")
	audit := flag.String("audit", "", "write every resolved candidate to this CSV for review")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	st, err := getchutitlerefs.Run(context.Background(), getchutitlerefs.Opts{
		DSN: *dsn, GetchuDSN: *getchuDSN, Apply: *apply,
		ProbableToo: *probableToo, Limit: *limit, Audit: *audit,
	})
	if st != nil {
		slog.Info("mint-getchu-title-refs done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("mint-getchu-title-refs", "error", err)
		os.Exit(1)
	}
}
