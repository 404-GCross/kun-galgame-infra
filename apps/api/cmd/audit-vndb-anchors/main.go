package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED, never inferred from the environment")
	apply := flag.Bool("apply", false, "write dead_at (default: report-only dry run)")
	minMirrorRows := flag.Int64("min-mirror-rows", defaultMinMirrorRows,
		"refuse to --apply unless src_vndb.vn holds at least this many rows (mid-reload guard)")
	flag.Parse()

	if *dsn == "" {
		slog.Error("audit-vndb-anchors", "error", "--dsn is required")
		os.Exit(1)
	}
	if err := run(context.Background(), *dsn, *apply, *minMirrorRows, os.Stdout); err != nil {
		slog.Error("audit-vndb-anchors", "error", err)
		os.Exit(1)
	}
}
