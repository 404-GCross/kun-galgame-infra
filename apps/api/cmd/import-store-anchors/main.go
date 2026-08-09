// import-store-anchors writes RELEASE-GRAIN store identity anchors (Steam /
// DMM / DLsite) from VNDB's own curated release extlinks (wave 197). The store
// id sits on the release VNDB itself anchors, so the ref lands EXACT — the
// wave-167 getchu argument at three more storefronts.
//
// Prerequisites: `ingest-vndb --only extlinks` and `--only releases_extlinks`
// must have staged those dump tables into the catalog DB, and catalog_source
// must carry the steam / dmm / dlsite rows (seed).
//
// Dry-run is the DEFAULT; pass --apply to write.
//
//	go run ./cmd/import-store-anchors --dsn "$CATALOG"
//	go run ./cmd/import-store-anchors --dsn "$CATALOG" --only steam --apply
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/jobs/storeanchors"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; also hosts src_vndb)")
	only := flag.String("only", "", "run a single lane: steam | dmm | dlsite | dlsite-en (default: all)")
	limit := flag.Int("limit", 0, "max candidates per lane (0 = all)")
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	flag.Parse()

	logger.Init("development")
	st, err := storeanchors.Run(context.Background(), storeanchors.Opts{
		Apply: *apply, DSN: *dsn, Only: *only, Limit: *limit,
	})
	if err != nil {
		slog.Error("import-store-anchors failed", "error", err)
		os.Exit(1)
	}
	report(st, *apply)
}

func report(st *storeanchors.Stats, apply bool) {
	mode := "DRY"
	if apply {
		mode = "APPLY"
	}
	fmt.Printf("\n=== import-store-anchors %s ===\n", mode)
	var totalPlanned, totalWritten int
	for _, name := range st.Order {
		ls := st.Lanes[name]
		totalPlanned += ls.Planned
		totalWritten += ls.Written
		fmt.Printf("%-10s candidates=%d planned=%d written=%d conflict=%d errors=%d\n",
			name, ls.Candidates, ls.Planned, ls.Written, ls.Conflict, ls.Errors)
		fmt.Printf("%-10s skipped: malformed=%d rejection=%d value_taken=%d ambiguous=%d dedup=%d\n",
			"", ls.SkippedMalformed, ls.SkippedRejection,
			ls.SkippedValueTaken, ls.SkippedAmbiguous, ls.SkippedDedup)
		if len(ls.TakenSamples) > 0 {
			fmt.Printf("%-10s value_taken e.g. %s\n", "", strings.Join(ls.TakenSamples, ", "))
		}
		if len(ls.AmbiguousSamples) > 0 {
			fmt.Printf("%-10s ambiguous e.g. %s\n", "", strings.Join(ls.AmbiguousSamples, ", "))
		}
	}
	fmt.Printf("TOTAL      planned=%d written=%d\n", totalPlanned, totalWritten)
}
