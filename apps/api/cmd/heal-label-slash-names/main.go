package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
)

const reindexNote = "search: run `reindex-catalog --index=catalog_labels` (full sweep; there is no incremental label indexer)"

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED, never inferred from the environment")
	apply := flag.Bool("apply", false, "write (default: dry-run preview)")
	flag.Parse()

	if *dsn == "" {
		slog.Error("heal-label-slash-names", "error", "--dsn is required")
		os.Exit(1)
	}
	if err := run(*dsn, *apply); err != nil {
		slog.Error("heal-label-slash-names", "error", err)
		os.Exit(1)
	}
}

func run(dsn string, apply bool) error {
	db, err := database.OpenJob(dsn)
	if err != nil {
		return fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	var healed, skipped int
	for _, c := range healCases {
		res, err := applyCase(db, c, apply, os.Stdout)
		if err != nil {
			return fmt.Errorf("label %d: %w", c.LabelID, err)
		}
		if res.skipped {
			skipped++
			continue
		}
		healed++
	}
	verb := "would heal"
	if apply {
		verb = "healed"
	}
	fmt.Fprintf(os.Stdout, "\n[heal-label-slash-names] %s=%d skipped=%d (of %d adjudicated cases)\n",
		verb, healed, skipped, len(healCases))
	if apply && healed > 0 {
		fmt.Fprintf(os.Stdout, "[heal-label-slash-names] %s\n", reindexNote)
	}
	return nil
}
