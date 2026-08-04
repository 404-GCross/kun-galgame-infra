// backfill-getchu-portraits fills catalog CHARACTER portraits from the Getchu
// crawler's mirrored bust crops (refs/proj/167 §10).
//
// Bytes come from a LOCAL mirror produced by kun-getchu-api:
//
//	getchu-crawler mirror --out DIR --kind nameplate --ids-file need-ids.txt
//
// This binary never dials getchu.com. Both DSNs are REQUIRED.
//
//	go run ./cmd/backfill-getchu-portraits --dsn "$CATALOG" --getchu-dsn "$GETCHU" --mirror-dir DIR
//	go run ./cmd/backfill-getchu-portraits --dsn "$CATALOG" --getchu-dsn "$GETCHU" --mirror-dir DIR --apply
//
// A dry run needs no mirror at all: it reports how many characters resolve and
// how many of those are still missing bytes, which is exactly the list to mirror.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/getchuportraits"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "upload and write (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	getchuDSN := flag.String("getchu-dsn", "", "getchu staging DSN — REQUIRED")
	mirrorDir := flag.String("mirror-dir", "", "local mirror root — REQUIRED to apply")
	limit := flag.Int("limit", 0, "max characters to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many characters first")
	gap := flag.Duration("upload-gap", 0, "min delay between uploads (raise for a long production sweep)")
	workers := flag.Int("workers", 1, "how many characters upload concurrently (1 = serial)")
	imageBase := flag.String("image-base", "", "image_service base URL override (local dev)")
	idsOut := flag.String("ids-out", "", "write the distinct Getchu ids the candidates need to this file (the crawler's --ids-file)")
	auditOut := flag.String("audit-out", "", "write the falsification set (characters that already have a portrait AND a Getchu bust) as CSV")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	st, err := getchuportraits.Run(context.Background(), cfg, getchuportraits.Opts{
		DSN: *dsn, GetchuDSN: *getchuDSN, MirrorDir: *mirrorDir, Apply: *apply,
		Limit: *limit, Offset: *offset,
		UploadGap: *gap, ImageBase: *imageBase, Workers: *workers, IDsOut: *idsOut, AuditOut: *auditOut,
	})
	if st != nil {
		slog.Info("backfill-getchu-portraits done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("backfill-getchu-portraits", "error", err)
		os.Exit(1)
	}
}
