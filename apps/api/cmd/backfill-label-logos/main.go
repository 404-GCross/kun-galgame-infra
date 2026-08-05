// backfill-label-logos fills catalog_label.logo_hash — a brand's logo or a
// circle's avatar — from a LOCAL mirror produced by one of the crawler repos
// (wave 170, refs/proj/170).
//
// --source picks WHICH upstream, and has no default:
//
//	--source bangumi   api.bgm.tv person type=2 (会社)   <mirror>/<id>/logo.<ext>
//	--source cien      ci-en.net creator profile avatar  <mirror>/<id>/avatar.<ext>
//
// PRECEDENCE IS THE RUN ORDER. Bangumi goes first in production; Ci-en then
// only fills what is still empty, because the candidate filter is literally
// an empty logo_hash. There is no ranking code to disagree with that, and the
// UPDATE re-asserts the same condition, so a late cien run can never overwrite
// a bangumi logo.
//
// This binary NEVER dials Bangumi or Ci-en — fetching is the crawler repos'
// job. --dsn is REQUIRED: a bare run cannot touch a live database.
//
//	go run ./cmd/backfill-label-logos --source bangumi --dsn "$CATALOG" --ids-out need-ids.txt
//	go run ./cmd/backfill-label-logos --source bangumi --dsn "$CATALOG" --mirror-dir DIR
//	go run ./cmd/backfill-label-logos --source bangumi --dsn "$CATALOG" --mirror-dir DIR --apply
//
// A DRY RUN (the default) needs no mirror at all: it reports how many labels
// carry an exact anchor with no logo yet, how many of those already have bytes
// on disk, and how many are still missing them — and --ids-out writes exactly
// the ids still needing bytes, which is the crawler's --ids-file.
//
// APPLY uploads the mirrored bytes to the image service catalog scope
// (KUN_CATALOG_IMAGE_CLIENT_ID/SECRET; --image-base points at a local dev
// service), writes logo_hash, records provenance under field_provenance
// ["logo_hash"], bumps updated_at, and reference-pings the fresh hashes so the
// bytes do not sit at upload-time TTL waiting for the nightly sweep. It is
// idempotent: a label that already has a logo is skipped before any byte read.
//
// --audit-out writes the falsification set: the labels anchored to BOTH
// sources, i.e. the only ones where the bangumi > cien ruling actually decides
// anything, for the human precedence review.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/labellogos"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	sourceName := flag.String("source", "", "which upstream supplies the bytes: bangumi (brand logos) or cien (creator avatars) — REQUIRED")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	mirrorDir := flag.String("mirror-dir", "", "local mirror root (<dir>/<external_id>/<logo|avatar>.<ext> + <dir>/dims.jsonl) — REQUIRED to apply")
	apply := flag.Bool("apply", false, "upload and write (default: dry-run forecast only)")
	limit := flag.Int("limit", 0, "max labels to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many labels first")
	idsOut := flag.String("ids-out", "", "write the distinct external ids still needing bytes to this file (the crawler's --ids-file)")
	auditOut := flag.String("audit-out", "", "write the falsification set (labels anchored to BOTH bangumi and cien) as CSV")
	imageBase := flag.String("image-base", "", "image_service base URL override (local dev)")
	gap := flag.Duration("upload-gap", 0, "min delay between uploads (raise for a long production sweep)")
	workers := flag.Int("workers", 1, "how many labels upload concurrently (1 = serial)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	source, err := labellogos.ParseSource(*sourceName)
	if err != nil {
		slog.Error("backfill-label-logos", "error", err)
		os.Exit(1)
	}

	st, err := labellogos.Run(context.Background(), cfg, labellogos.Opts{
		Source: source, DSN: *dsn, MirrorDir: *mirrorDir, Apply: *apply,
		Limit: *limit, Offset: *offset,
		UploadGap: *gap, ImageBase: *imageBase, Workers: *workers,
		IDsOut: *idsOut, AuditOut: *auditOut,
	})
	if st != nil {
		slog.Info("backfill-label-logos done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("backfill-label-logos", "error", err)
		os.Exit(1)
	}
}
