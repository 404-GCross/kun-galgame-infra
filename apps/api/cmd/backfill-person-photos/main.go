// backfill-person-photos fills catalog_person.photo_hash — the photograph of a
// real-world individual behind credit names — from a LOCAL mirror produced by
// the kun-bangumi-api crawler (wave 172).
//
// ONE SOURCE, bangumi. Only Bangumi publishes a person picture, so unlike the
// label-logo lane this one has no precedence question and no --source flag. The
// mirror layout is nevertheless identical, because the SAME crawler command
// (fetch-person-images) produces it:
//
//	<mirror>/<external_id>/logo.<ext>   ext ∈ jpg|jpeg|png|webp|gif
//	<mirror>/dims.jsonl                 optional {"id","file","w","h","url"} manifest
//
// THE ANCHOR IS EXACT AND IT IS THE PERSON'S OWN. catalog_external_ref with
// entity_type=person, source_id=bangumi, link_kind=exact — never a credit-name
// anchor (that would smuggle in the identity-resolution judgment the entity
// layer keeps explicit), never a probable or related one (a guessed link puts a
// stranger's face on this person's page).
//
// This binary NEVER dials Bangumi — fetching is the crawler repo's job. --dsn is
// REQUIRED: a bare run cannot touch a live database.
//
//	go run ./cmd/backfill-person-photos --dsn "$CATALOG" --ids-out need-ids.txt
//	go run ./cmd/backfill-person-photos --dsn "$CATALOG" --mirror-dir DIR
//	go run ./cmd/backfill-person-photos --dsn "$CATALOG" --mirror-dir DIR --apply
//
// A DRY RUN (the default) needs no mirror at all: it reports how many persons
// carry an exact bangumi anchor with no photo yet, how many of those already
// have bytes on disk, and how many are still missing them — and --ids-out writes
// exactly the ids still needing bytes, which is the crawler's --ids-file.
//
// APPLY uploads the mirrored bytes to the image service catalog scope
// (KUN_CATALOG_IMAGE_CLIENT_ID/SECRET; --image-base points at a local dev
// service), writes photo_hash, records provenance under
// field_provenance["photo_hash"], bumps updated_at, and reference-pings the
// fresh hashes so the bytes do not sit at upload-time TTL waiting for the
// nightly sweep. It is idempotent: a person that already has a photo is skipped
// before any byte read.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/personphotos"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	mirrorDir := flag.String("mirror-dir", "", "local mirror root (<dir>/<external_id>/logo.<ext> + <dir>/dims.jsonl) — REQUIRED to apply")
	apply := flag.Bool("apply", false, "upload and write (default: dry-run forecast only)")
	limit := flag.Int("limit", 0, "max persons to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many persons first")
	idsOut := flag.String("ids-out", "", "write the distinct external ids still needing bytes to this file (the crawler's --ids-file)")
	imageBase := flag.String("image-base", "", "image_service base URL override (local dev)")
	gap := flag.Duration("upload-gap", 0, "min delay between uploads (raise for a long production sweep)")
	workers := flag.Int("workers", 1, "how many persons upload concurrently (1 = serial)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	st, err := personphotos.Run(context.Background(), cfg, personphotos.Opts{
		DSN: *dsn, MirrorDir: *mirrorDir, Apply: *apply,
		Limit: *limit, Offset: *offset,
		UploadGap: *gap, ImageBase: *imageBase, Workers: *workers,
		IDsOut: *idsOut,
	})
	if st != nil {
		slog.Info("backfill-person-photos done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("backfill-person-photos", "error", err)
		os.Exit(1)
	}
}
