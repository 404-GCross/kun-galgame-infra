// backfill-alias-source attributes the PRE-wave-195 alias corpus: it fills
// catalog_{character,name,label}_alias.source_id for the rows whose writer is
// provably unique, and leaves every other row NULL.
//
// This is a ONE-SHOT, not a maintenance job. It exists because wave 195 added
// source_id to tables that had been accumulating rows for a year without it,
// and the only evidence of where those rows came from is which code could have
// written them. That evidence is readable today and gets weaker with every new
// writer, which is why the wave that adds the column also spends it.
//
// The two rules it applies, each verified against the writer set at the time
// of the wave (see refs/proj/195):
//
//	kind=0 (translation), all three tables → bangumi (3)
//	    internal/jobs/bgmzhnames is the ONLY producer of translation rows, in
//	    all three lanes, and it draws exclusively from the Bangumi infobox.
//	    Confirmed by the data: every kind=0 row in every table is zh-Hans.
//
//	catalog_character_alias kind=1 (spelling variant) → vndb (2)
//	    the VNDB roster importer is the only writer of character spelling
//	    variants (internal/platform/catalog/importer/rostervndb.go).
//
// Everything else stays NULL ON PURPOSE. catalog_label_alias kind=1 comes out
// of internal/jobs/orglabels, which mixes VNDB producer aliases with other
// legs; the kind=2 search hints mix Bangumi infoboxes, erogamespace
// parentheticals and a first-party heal. Guessing a source for those would
// write the exact kind of unattributable claim the column was added to prevent
// — a NULL that says "not recorded" is worth more than a plausible wrong id.
//
// ⚠️ Do NOT re-run this after a second translation source starts writing kind=0
// rows: the kind=0 → bangumi rule is a fact about the writer set of wave 195,
// not a property of the schema. It is deliberately NOT part of cmd/migrate-catalog
// for that reason. The WHERE clauses only touch source_id IS NULL rows, so a
// re-run today is a harmless no-op.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted.
//
//	go run ./cmd/backfill-alias-source \
//	    --dsn "host=localhost port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	go run ./cmd/backfill-alias-source --apply --dsn "..."
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/platform/catalog/model"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	sourceVNDB    int16 = 2
	sourceBangumi int16 = 3
)

// rule is one attributable slice of the corpus: the rows `where` selects in
// `table` were written by a single known job, so they can carry `source`.
type rule struct {
	table  string
	where  string
	args   []any
	source int16
	why    string
}

func rules() []rule {
	var out []rule
	for _, t := range []string{"catalog_character_alias", "catalog_name_alias", "catalog_label_alias"} {
		out = append(out, rule{
			table: t, where: "kind = ?", args: []any{model.AliasKindTranslation},
			source: sourceBangumi, why: "bgmzhnames is the sole writer of translation rows",
		})
	}
	out = append(out, rule{
		table: "catalog_character_alias", where: "kind = ?", args: []any{model.AliasKindSpellingVariant},
		source: sourceVNDB, why: "the VNDB roster importer is the sole writer of character spelling variants",
	})
	return out
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counts only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED, never defaulted")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	if *dsn == "" {
		slog.Error("backfill-alias-source: --dsn is required")
		os.Exit(1)
	}
	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{})
	if err != nil {
		slog.Error("backfill-alias-source: open", "error", err)
		os.Exit(1)
	}

	var attributed, remaining int64
	byTable := map[string]int64{}
	for _, r := range rules() {
		// source_id IS NULL keeps this idempotent AND keeps it off any row a
		// writer has already attributed itself.
		where := "source_id IS NULL AND " + r.where

		var n int64
		if err := db.Raw(
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s`, r.table, where), r.args...,
		).Scan(&n).Error; err != nil {
			slog.Error("backfill-alias-source: count", "table", r.table, "error", err)
			os.Exit(1)
		}
		if *apply {
			res := db.Exec(
				fmt.Sprintf(`UPDATE %s SET source_id = ? WHERE %s`, r.table, where),
				append([]any{r.source}, r.args...)...)
			if res.Error != nil {
				slog.Error("backfill-alias-source: update", "table", r.table, "error", res.Error)
				os.Exit(1)
			}
			n = res.RowsAffected
		}
		attributed += n
		byTable[r.table] += n
		slog.Info("backfill-alias-source rule", "table", r.table, "source", r.source,
			"rows", n, "why", r.why)
	}

	// What is deliberately left unattributed, so the run reports its own limits
	// instead of reading as full coverage. On a dry run the rule rows are still
	// NULL in the table, so they are subtracted here rather than counted twice.
	for _, t := range []string{"catalog_character_alias", "catalog_name_alias", "catalog_label_alias"} {
		var n int64
		if err := db.Raw(fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE source_id IS NULL`, t)).Scan(&n).Error; err != nil {
			slog.Error("backfill-alias-source: residual count", "table", t, "error", err)
			os.Exit(1)
		}
		if !*apply {
			n -= byTable[t]
		}
		remaining += n
		slog.Info("backfill-alias-source unattributed", "table", t, "rows", n)
	}

	slog.Info("backfill-alias-source done", "apply", *apply,
		"attributed", attributed, "left_null", remaining)
}
