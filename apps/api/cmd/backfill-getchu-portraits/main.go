// backfill-getchu-portraits fills a catalog character's images from the Getchu
// crawler's mirrored character art (refs/proj/167 §10 and §11).
//
// --slot picks WHICH image, and has no default:
//
//	--slot bust     charaN.jpg   250x300 upper-body crop  -> image_hash
//	--slot figure   charabN.jpg  500x500 full-body art    -> figure_hash
//
// Bytes come from a LOCAL mirror produced by kun-getchu-api (note the crawler
// kind names are the opposite of the slot names — see the package doc):
//
//	getchu-crawler mirror --out DIR --kind nameplate --ids-file need-ids.txt   # bust
//	getchu-crawler mirror --out DIR --kind portrait  --ids-file need-ids.txt   # figure
//
// This binary never dials getchu.com. Both DSNs are REQUIRED.
//
//	go run ./cmd/backfill-getchu-portraits --slot figure --dsn "$CATALOG" --getchu-dsn "$GETCHU" --mirror-dir DIR
//	go run ./cmd/backfill-getchu-portraits --slot figure --dsn "$CATALOG" --getchu-dsn "$GETCHU" --mirror-dir DIR --apply
//
// A dry run needs no mirror at all: it reports how many characters resolve and
// how many of those are still missing bytes — and --ids-out writes exactly the
// list to mirror.
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
	slotName := flag.String("slot", "", "which image to fill: bust (->image_hash) or figure (->figure_hash) — REQUIRED")
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

	slot, err := getchuportraits.ParseSlot(*slotName)
	if err != nil {
		slog.Error("backfill-getchu-portraits", "error", err)
		os.Exit(1)
	}

	st, err := getchuportraits.Run(context.Background(), cfg, getchuportraits.Opts{
		DSN: *dsn, GetchuDSN: *getchuDSN, Slot: slot, MirrorDir: *mirrorDir, Apply: *apply,
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
