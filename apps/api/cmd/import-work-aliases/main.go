// import-work-aliases backfills bodyless works' alias titles (step 95): bgm
// infobox 别名 → kind=alias rows; dlsite work_name_kana → kind=search_hint
// rows for the anchored tail lacking one. Dry-run by default; ON CONFLICT DO
// NOTHING (aliases are static facts — re-runs are no-ops).
//
//	go run ./cmd/import-work-aliases --dsn "$CATALOG" --dlsite-dsn "$DLSITE" [--source bgm|dlsite-kana|all] [--apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/workaliases"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED; hosts src_bangumi)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "dlsite mirror DSN (REQUIRED for dlsite-kana/all)")
	source := flag.String("source", "all", "lane: bgm | dlsite-kana | all")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := workaliases.Run(context.Background(), workaliases.Opts{
		Apply: *apply, DSN: *dsn, DlsiteDSN: *dlsiteDSN, Source: *source,
	})
	if err != nil {
		slog.Error("import-work-aliases failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-work-aliases %s ===\n", mode(*apply))
	fmt.Printf("bgm: works=%d planned=%d skipped_dup=%d written=%d conflict=%d\n",
		st.BgmWorks, st.BgmPlanned, st.BgmSkippedDup, st.BgmWritten, st.BgmConflict)
	fmt.Printf("kana: works=%d no_kana=%d planned=%d written=%d\n",
		st.KanaWorks, st.KanaNoKana, st.KanaPlanned, st.KanaWritten)
	fmt.Printf("errors=%d\n", st.Errors)
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
