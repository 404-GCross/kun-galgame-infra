// import-work-series materializes dlsite work series into catalog_series /
// catalog_series_member (step 94, refs/proj/94 option B). Only series with
// >=2 anchored galgame works land. Dry-run by default; refresh-runnable
// (series rename in place, member insert-if-absent + stale delete, sub-gate
// series deleted — the mirror is the truth).
//
//	go run ./cmd/import-work-series --dsn "$CATALOG" --dlsite-dsn "$DLSITE" [--apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/workseries"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "dlsite mirror DSN (REQUIRED)")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := workseries.Run(context.Background(), workseries.Opts{
		Apply: *apply, DSN: *dsn, DlsiteDSN: *dlsiteDSN,
	})
	if err != nil {
		slog.Error("import-work-series failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-work-series %s ===\n", mode(*apply))
	fmt.Printf("anchored_works=%d series_eligible=%d members_wanted=%d\n", st.AnchoredWorks, st.SeriesEligible, st.MembersWanted)
	fmt.Printf("series: created=%d renamed=%d deleted=%d\n", st.SeriesCreated, st.SeriesRenamed, st.SeriesDeleted)
	fmt.Printf("members: added=%d stale=%d\n", st.MembersAdded, st.MembersStale)
	fmt.Printf("errors=%d\n", st.Errors)
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
