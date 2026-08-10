package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

const (
	sourceVNDB    int16 = 2
	sourceBangumi int16 = 3
)

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

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	if *dsn == "" {
		slog.Error("backfill-alias-source: --dsn is required")
		os.Exit(1)
	}
	db, err := database.OpenJobWith(*dsn, &gorm.Config{})
	if err != nil {
		slog.Error("backfill-alias-source: open", "error", err)
		os.Exit(1)
	}

	var attributed, remaining int64
	byTable := map[string]int64{}
	for _, r := range rules() {
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
