package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/storerefs"
	"api/pkg/logger"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED)")
	egDSN := flag.String("eg-dsn", "", "EG mirror DSN (REQUIRED)")
	apply := flag.Bool("apply", false, "write changes (default dry)")
	flag.Parse()

	logger.Init("development")
	st, err := storerefs.Run(context.Background(), storerefs.Opts{
		Apply: *apply, DSN: *dsn, EGDSN: *egDSN,
	})
	if err != nil {
		slog.Error("import-store-refs failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("\n=== import-store-refs %s ===\nanchored=%d\n", mode(*apply), st.Anchored)
	fmt.Printf("steam: planned=%d written=%d exists=%d\n", st.SteamPlanned, st.SteamWritten, st.SteamExists)
	fmt.Printf("dmm: planned=%d written=%d exists=%d\n", st.DmmPlanned, st.DmmWritten, st.DmmExists)
	fmt.Printf("rejected=%d errors=%d\n", st.Rejected, st.Errors)
}

func mode(apply bool) string {
	if apply {
		return "APPLY"
	}
	return "DRY"
}
