// cmd/import-trust-terms bulk-loads a Tier0 word list into the kun_trust
// database's trust_term registry (doc 18 §6; step 08). The admin CRUD face
// registers terms one at a time — unfit for the tens of thousands of entries a
// lexicon import needs — so this tool takes plain-text files (one term per
// line) → norm.Normalize (the SINGLE choke point, reused, never reimplemented)
// → rune-floor filter → dedup (in-batch + against the DB's active terms) →
// batched INSERT.
//
// Usage:
//
//	go run ./cmd/import-trust-terms [flags] <file.txt ...>
//
// Flags:
//
//	-site       target scope; "" (default) = a GLOBAL term (stored site NULL).
//	-kind       enforcement intent: 0=suspect (default) / 1=banned (explicit).
//	-note       operator memo; "" (default) = the per-file source filename.
//	-min-runes  drop terms whose POST-Normalize rune count is below this (3).
//	-apply      write; default is a dry-run preview (nothing is inserted).
//
// The tool is idempotent: a second -apply run of the same files inserts 0 rows
// (every term is found in the existing-scope set). It never deprecates or
// updates an existing row — a term already active is simply skipped. It changes
// no schema and runs no migration; the trust migration must already have
// provisioned trust_term (cmd/migrate-trust).
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

	// An empty -site is a global term (site NULL); a non-empty value scopes it.
	var scope *string
	if *site != "" {
		scope = site
	}

	ic := importConfig{
		site:     scope,
		kind:     int16(*kind),
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
