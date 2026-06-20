// sync-vndb-screenshots fills the gallery of every PUBLISHED galgame that has no
// VNDB screenshots yet from the VNDB API, uploading each into image_service
// (site=galgame_wiki, preset galgame_screenshot) and inserting galgame_screenshot
// rows (source="vndb"). Fill-only; a user's own screenshots are never touched.
//
// Thin shell: logic lives in internal/jobs/vndbscreenshots (single source of
// truth) so this CLI and the scheduled "sync-vndb-screenshots" job run identical
// code. The job runs daily for the trickle of newly-claimed games; the one-time
// bulk was an offline dump backfill (cmd/migrate-galgame-screenshots). Use this
// CLI for a manual sweep or a targeted top-up:
//
//	go run ./cmd/sync-vndb-screenshots                  # dry run, count gaps
//	go run ./cmd/sync-vndb-screenshots --apply          # fill new games (job same)
//	go run ./cmd/sync-vndb-screenshots --apply --ids 1,2 # targeted top-up (any status)
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
	ids := flag.String("ids", "", "comma-separated galgame ids (targeted; any status; enables top-up of partially-filled games)")
	limit := flag.Int("limit", 0, "max galgames to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many galgames (for chunking)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
	imageBaseURL := flag.String("image-base-url", "", "image_service base override (default: the client's configured base)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if _, err := jobs.RunSyncVNDBScreenshots(context.Background(), cfg, jobs.SyncVNDBScreenshotsOpts{
		Apply:        *apply,
		Gap:          *gap,
		IDs:          parseIDs(*ids),
		Limit:        *limit,
		Offset:       *offset,
		ImageBaseURL: *imageBaseURL,
	}); err != nil {
		slog.Error("sync-vndb-screenshots", "error", err)
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
