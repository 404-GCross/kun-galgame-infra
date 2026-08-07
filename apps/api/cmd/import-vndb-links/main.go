// import-vndb-links mints the wave-186 link facet: link_kind=related
// external refs on works / labels / persons, read from VNDB's extlink pool
// (plus the Bangumi infobox official-site sub-lane at work grain). Dry-run by
// default; ON CONFLICT DO NOTHING — a second --apply writes zero.
//
//	go run ./cmd/import-vndb-links --dsn "$CATALOG" [--only label] [--limit 100] [--apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/entitylinks"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (default: built from KUN_CATALOG_PG_* env)")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	only := flag.String("only", "", "run one lane: work | label | person (default all)")
	limit := flag.Int("limit", 0, "cap planned inserts per lane (0 = no cap); rehearsal aid")
	flag.Parse()

	logger.Init("development")
	catalogDSN, err := resolveDSN(*dsn)
	if err != nil {
		slog.Error("resolve catalog dsn", "error", err)
		os.Exit(1)
	}
	st, err := entitylinks.Run(context.Background(), entitylinks.Opts{
		Apply: *apply, DSN: catalogDSN, Only: *only, Limit: *limit,
	})
	if err != nil {
		slog.Error("import-vndb-links failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-vndb-links %s ===\n", mode(*apply))
	printLane("work", st.Work)
	printLane("label", st.Label)
	printLane("person", st.Person)
}

func printLane(name string, s entitylinks.LaneStats) {
	fmt.Printf("%-7s planned=%d written=%d skipped_dedup=%d skipped_rejection=%d skipped_identity=%d skipped_store=%d skipped_malformed=%d\n",
		name, s.Planned, s.Written, s.SkippedDedup, s.SkippedRejection,
		s.SkippedIdentity, s.SkippedStore, s.SkippedMalformed)
}

// resolveDSN keeps --dsn authoritative and otherwise builds the catalog DSN
// from KUN_CATALOG_PG_* the same way the service itself does (config.Load →
// CatalogDatabase.DSN), so an ops run inside the deployed container needs no
// secret on the command line.
func resolveDSN(flagDSN string) (string, error) {
	if flagDSN != "" {
		return flagDSN, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config for KUN_CATALOG_PG_* fallback: %w", err)
	}
	return cfg.CatalogDatabase.DSN(), nil
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
