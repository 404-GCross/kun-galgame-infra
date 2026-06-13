// sync-vndb-links enriches published galgames with curated VNDB store/official
// links (source="vndb"), reconciled idempotently against the current VNDB truth.
// User-added links are never touched.
//
// Thin shell: logic lives in internal/jobs/vndblinks (single source of truth) so
// this CLI and the scheduled "sync-vndb-links" job run identical code. The job
// runs daily in --only-missing mode (new/claimed games); use this CLI for a full
// manual reconcile or a targeted fix.
//
//	go run ./cmd/sync-vndb-links --only-missing       # dry run, just un-enriched games
//	go run ./cmd/sync-vndb-links --apply              # apply, full pass
//	go run ./cmd/sync-vndb-links --apply --ids 1,2,3  # targeted (any status)
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
	apply := flag.Bool("apply", false, "write changes (default: dry run, fetch + diff only)")
	onlyMissing := flag.Bool("only-missing", false, "only published games with no vndb link yet (new/claimed)")
	limit := flag.Int("limit", 0, "max galgames to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many galgames (for chunking)")
	ids := flag.String("ids", "", "comma-separated galgame ids to process (targeted; any status; overrides only-missing/limit/offset)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
	samples := flag.Int("samples", 6, "number of per-game link previews to print")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if _, err := jobs.RunSyncVNDBLinks(context.Background(), cfg, jobs.SyncVNDBLinksOpts{
		Apply:       *apply,
		Gap:         *gap,
		OnlyMissing: *onlyMissing,
		IDs:         parseIDs(*ids),
		Limit:       *limit,
		Offset:      *offset,
		Samples:     *samples,
	}); err != nil {
		slog.Error("sync-vndb-links", "error", err)
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
