// import-release-labels materialises release-level attribution (wave 200):
// vndb releases_producers dev/pub flags → catalog_release_label. No language
// gate — a release states its own edition, so the localisation and port
// publishers land here instead of being flattened onto the work. Dry-run by
// default; ON CONFLICT DO NOTHING — re-runs are no-ops.
//
//	go run ./cmd/import-release-labels --dsn "$CATALOG" [--apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/releaselabels"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; hosts src_vndb)")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := releaselabels.Run(context.Background(), releaselabels.Opts{Apply: *apply, DSN: *dsn})
	if err != nil {
		slog.Error("import-release-labels failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-release-labels %s ===\n", mode(*apply))
	fmt.Printf("planned: dev=%d pub=%d\n", st.DevPlanned, st.PubPlanned)
	fmt.Printf("written=%d skipped_dup=%d unresolved_pairs=%d errors=%d\n",
		st.Written, st.SkippedDup, st.Unresolved, st.Errors)
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
