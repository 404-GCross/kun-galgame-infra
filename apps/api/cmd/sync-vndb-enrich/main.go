// sync-vndb-enrich enriches published galgames with curated VNDB links + tags +
// developers(officials), reconciled idempotently against the current VNDB truth.
// The source="vndb" subset is sync-managed; user-curated links/tags/officials
// are preserved.
//
// Thin shell: logic lives in internal/jobs/vndbenrich (single source of truth)
// so this CLI and the scheduled "sync-vndb-enrich" job run identical code. The
// job runs daily in --only-missing mode (new/claimed games); use this CLI for a
// full manual reconcile or a targeted fix.
//
//	go run ./cmd/sync-vndb-enrich --only-missing       # dry run, just un-enriched games
//	go run ./cmd/sync-vndb-enrich --apply              # apply, full pass
//	go run ./cmd/sync-vndb-enrich --apply --ids 1,2,3  # targeted (any status)
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
	tagmap := flag.String("tagmap", "", "path to tagMap.ts (default: env KUN_VNDB_TAGMAP_PATH or docs/tagMap.ts)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if _, err := jobs.RunSyncVNDBEnrich(context.Background(), cfg, jobs.SyncVNDBEnrichOpts{
		Apply:       *apply,
		Gap:         *gap,
		OnlyMissing: *onlyMissing,
		IDs:         parseIDs(*ids),
		Limit:       *limit,
		Offset:      *offset,
		TagMapPath:  *tagmap,
	}); err != nil {
		slog.Error("sync-vndb-enrich", "error", err)
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
