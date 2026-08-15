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
	if st.Errors > 0 {
		os.Exit(1)
	}
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
