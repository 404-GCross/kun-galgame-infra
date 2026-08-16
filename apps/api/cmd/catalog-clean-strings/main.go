package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/textnorm"
)

type target struct{ table, idCol, textCol string }

var targets = []target{
	{"catalog_work", "id", "display_name"},
	{"catalog_work_title", "id", "title"},
	{"catalog_credit_name", "id", "name"},
	{"catalog_name_alias", "id", "name"},
	{"catalog_character", "id", "display_name"},
	{"catalog_character_alias", "id", "name"},
	{"catalog_label", "id", "display_name"},
	{"catalog_label_alias", "id", "name"},
	{"catalog_release", "id", "title"},
}

func main() {
	dsn := flag.String("dsn", "", "REQUIRED explicit target catalog DSN (never config; rehearse on kun_catalog_rehearsal)")
	apply := flag.Bool("apply", false, "actually write (default: dry-run preview)")
	logPath := flag.String("log", "catalog-clean-strings.log.tsv", "TSV change log path (old→new, the reverse record)")
	limit := flag.Int("limit", 0, "cap rows per target (0 = all); for smoke testing")
	flag.Parse()

	if *dsn == "" {
		slog.Error("--dsn is required (explicit target; never config)")
		os.Exit(2)
	}

	db, err := database.OpenJob(*dsn)
	if err != nil {
		slog.Error("db connect", "error", err)
		os.Exit(1)
	}

	logf, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Error("open log", "error", err)
		os.Exit(1)
	}
	defer logf.Close()
	fmt.Fprintf(logf, "# run %s apply=%v dsn-tail=%s\n", time.Now().Format(time.RFC3339), *apply, dsnTail(*dsn))
	fmt.Fprintln(logf, "table\tid\tcolumn\told\tnew")

	var totalCand, totalWritten int
	for _, t := range targets {
		q := fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s ORDER BY %s", t.idCol, t.textCol, t.table, textnorm.DirtyWhereSQL(t.textCol), t.idCol)
		if *limit > 0 {
			q += fmt.Sprintf(" LIMIT %d", *limit)
		}
		rows, err := db.Raw(q).Rows()
		if err != nil {
			slog.Error("query", "table", t.table, "error", err)
			os.Exit(1)
		}
		var cand, written int
		for rows.Next() {
			var id int64
			var val string
			if err := rows.Scan(&id, &val); err != nil {
				slog.Error("scan", "table", t.table, "error", err)
				os.Exit(1)
			}
			nv := textnorm.Clean(val)
			if nv == val || nv == "" {
				continue
			}
			cand++
			fmt.Fprintf(logf, "%s\t%d\t%s\t%s\t%s\n", t.table, id, t.textCol, strconv.Quote(val), strconv.Quote(nv))
			if *apply {
				res := db.Exec(
					fmt.Sprintf("UPDATE %s SET %s = ? WHERE %s = ? AND %s = ?", t.table, t.textCol, t.idCol, t.textCol),
					nv, id, val)
				if res.Error != nil {
					slog.Error("update", "table", t.table, "id", id, "error", res.Error)
					os.Exit(1)
				}
				written += int(res.RowsAffected)
			}
		}
		rows.Close()
		slog.Info("target", "table", t.table, "column", t.textCol, "candidates", cand, "written", written)
		totalCand += cand
		totalWritten += written
	}
	mode := "DRY-RUN (no writes)"
	if *apply {
		mode = "APPLIED"
	}
	slog.Info("done", "mode", mode, "candidates", totalCand, "written", totalWritten, "log", *logPath)
}

func dsnTail(dsn string) string {
	if i := strings.LastIndex(dsn, "/"); i >= 0 && i+1 < len(dsn) {
		return dsn[i+1:]
	}
	return "?"
}
