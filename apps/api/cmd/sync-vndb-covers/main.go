// sync-vndb-covers fills the cover GALLERY of PUBLISHED galgames from VNDB — the
// main cover (vn.image) plus every release cover (pkgfront/dig/pkgback/…) — into
// image_service (site=galgame_wiki) + galgame_cover (each labeled with `kind`,
// source="vndb"). One stays pinned (sort_order=0); the rest are candidates. A
// user's own covers are never touched; re-runs are idempotent.
//
// Thin shell: logic lives in internal/jobs/vndbcovers (single source of truth) so
// this CLI and the scheduled "sync-vndb-covers" job run identical code. The job
// runs daily in API mode for cover-less (new/claimed) games. Use this CLI for the
// one-time BULK backfill from the offline dump (--all = also add release covers to
// games that already have a cover):
//
//	go run ./cmd/sync-vndb-covers                       # dry run, API, count gaps
//	go run ./cmd/sync-vndb-covers --apply --all \       # full bulk backfill from the dump
//	    --vndb-image-dir=/data/vndb-img --vn-dump=/data/db/vn --images-dump=/data/db/images \
//	    --rel-vn-dump=/data/db/releases_vn --rel-images-dump=/data/db/releases_images
//	go run ./cmd/sync-vndb-covers --apply --ids 1,2,3   # targeted (any status)
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"api/internal/jobs"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run — resolve + count only)")
	all := flag.Bool("all", false, "process EVERY published vndb game (the one-time backfill), not just cover-less ones")
	concurrency := flag.Int("concurrency", 1, "parallel per-game upload workers (raise for the dump backfill, e.g. 16; keep 1 for API mode)")
	ids := flag.String("ids", "", "comma-separated galgame ids (targeted; any status; overrides published-only)")
	limit := flag.Int("limit", 0, "max galgames to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many galgames (for chunking)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls (API mode)")
	vndbImageDir := flag.String("vndb-image-dir", "", "rsync mirror of rsync://dl.vndb.org/vndb-img (must contain cv/); set => DUMP mode, else VNDB API")
	vnDump := flag.String("vn-dump", "./db/vn", "dump's db/vn file (dump mode)")
	imagesDump := flag.String("images-dump", "./db/images", "dump's db/images file (dump mode)")
	relVNDump := flag.String("rel-vn-dump", "./db/releases_vn", "dump's db/releases_vn file (dump mode)")
	relImagesDump := flag.String("rel-images-dump", "./db/releases_images", "dump's db/releases_images file (dump mode)")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (default: the client's configured base)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if _, err := jobs.RunSyncVNDBCovers(context.Background(), cfg, jobs.SyncVNDBCoversOpts{
		Apply:             *apply,
		All:               *all,
		Concurrency:       *concurrency,
		Gap:               *gap,
		IDs:               parseIDs(*ids),
		Limit:             *limit,
		Offset:            *offset,
		VNDBImageDir:      *vndbImageDir,
		VNDumpPath:        *vnDump,
		ImagesDumpPath:    *imagesDump,
		RelVNDumpPath:     *relVNDump,
		RelImagesDumpPath: *relImagesDump,
		ImageBaseURL:      *imageBaseURL,
	}); err != nil {
		slog.Error("sync-vndb-covers", "error", err)
		os.Exit(1)
	}
}

// parseIDs turns "2167, 4121,4793" into []int{2167,4121,4793}, skipping blanks
// and non-numbers.
func parseIDs(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	return out
}
