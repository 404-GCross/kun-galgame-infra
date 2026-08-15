package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/jobs/imagegradesync"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (kun_catalog) — REQUIRED, never inferred from the environment")
	imagesDSN := flag.String("images-dsn", "", "image service DSN (kun_images) — REQUIRED")
	apply := flag.Bool("apply", false, "write (default: dry-run forecast)")
	limit := flag.Int("limit", 0, "max media rows to scan (0 = all)")
	batch := flag.Int("batch", 5000, "rows per page")
	source := flag.String("source", "", "restrict to one catalog_source key (default: every machine-ingested source)")
	flag.Parse()

	st, err := imagegradesync.Run(context.Background(), imagegradesync.Opts{
		DSN: *dsn, ImagesDSN: *imagesDSN, Apply: *apply,
		Limit: *limit, Batch: *batch, Source: *source,
	})
	if st != nil {
		fmt.Fprintf(os.Stdout, "%s\n", st.Matrix())
		slog.Info("sync-image-grades done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("sync-image-grades", "error", err)
		os.Exit(1)
	}
	if st != nil && st.Errors > 0 {
		os.Exit(1)
	}
}
