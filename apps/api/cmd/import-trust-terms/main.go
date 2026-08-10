package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/trust/model"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	site := flag.String("site", "", `target site scope ("" = global, stored site NULL)`)
	kind := flag.Int("kind", int(model.TermKindSuspect), "enforcement intent: 0=suspect, 1=banned")
	purpose := flag.Int("purpose", int(model.TermPurposeAbuse), "why listed: 0=abuse, 1=compliance")
	note := flag.String("note", "", "operator memo (default: the source filename, per file)")
	minRunes := flag.Int("min-runes", 3, "drop terms with fewer runes than this AFTER normalization")
	apply := flag.Bool("apply", false, "write to the database (default: dry-run preview)")
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "usage: import-trust-terms [flags] <file.txt ...>")
		flag.PrintDefaults()
		os.Exit(2)
	}
	if *purpose != int(model.TermPurposeAbuse) && *purpose != int(model.TermPurposeCompliance) {
		fmt.Fprintf(os.Stderr, "invalid -purpose %d: must be 0 (abuse) or 1 (compliance)\n", *purpose)
		os.Exit(2)
	}
	if *kind != int(model.TermKindSuspect) && *kind != int(model.TermKindBanned) {
		fmt.Fprintf(os.Stderr, "invalid -kind %d: must be 0 (suspect) or 1 (banned)\n", *kind)
		os.Exit(2)
	}
	if *minRunes < 0 {
		fmt.Fprintf(os.Stderr, "invalid -min-runes %d: must be >= 0\n", *minRunes)
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	trustDB, err := database.NewPostgresDB(cfg.TrustDatabase)
	if err != nil {
		slog.Error("failed to connect to trust database", "error", err)
		os.Exit(1)
	}
	defer trustDB.Close()

	var scope *string
	if *site != "" {
		scope = site
	}

	ic := importConfig{
		site:     scope,
		kind:     int16(*kind),
		purpose:  int16(*purpose),
		note:     *note,
		minRunes: *minRunes,
		apply:    *apply,
	}

	if _, err := run(trustDB.DB(), os.Stdout, ic, files); err != nil {
		slog.Error("import failed", "error", err)
		os.Exit(1)
	}

	if !*apply {
		fmt.Fprintln(os.Stdout, "\nDRY-RUN — nothing written; re-run with -apply to insert.")
	}
}
