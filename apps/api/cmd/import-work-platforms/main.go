// import-work-platforms backfills platform facts (step 96): dlsite mirror
// product_json.platform → catalog_release.platform + extra.platforms for the
// anchored galgame stubs (the step-76 shape); bgm infobox 平台 →
// catalog_work_platform rows for bodyless works. Dry-run by default; dlsite
// writes are guarded on platform still being empty, bgm writes ON CONFLICT
// DO NOTHING — re-runs are no-ops.
//
//	go run ./cmd/import-work-platforms --dsn "$CATALOG" --dlsite-dsn "$DLSITE" [--source dlsite|bgm|all] [--apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"api/internal/jobs/workplatforms"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; hosts src_bangumi)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "dlsite mirror DSN (REQUIRED for dlsite/all)")
	source := flag.String("source", "all", "lane: dlsite | bgm | all")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := workplatforms.Run(context.Background(), workplatforms.Opts{
		Apply: *apply, DSN: *dsn, DlsiteDSN: *dlsiteDSN, Source: *source,
	})
	if err != nil {
		slog.Error("import-work-platforms failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-work-platforms %s ===\n", mode(*apply))
	fmt.Printf("dlsite: candidates=%d no_mirror=%d planned=%d written=%d raced=%d\n",
		st.DlCandidates, st.DlNoMirror, st.DlPlanned, st.DlWritten, st.DlRaced)
	fmt.Printf("bgm: works=%d planned=%d written=%d conflict=%d\n",
		st.BgmWorks, st.BgmPlanned, st.BgmWritten, st.BgmConflict)
	if len(st.Unmapped) > 0 {
		fmt.Printf("unmapped 平台 values (top 20): %s\n", strings.Join(st.TopUnmapped(20), " "))
	}
	fmt.Printf("errors=%d\n", st.Errors)
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
