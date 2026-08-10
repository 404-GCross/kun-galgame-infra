package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"api/internal/infrastructure/database"
)

type row struct {
	LabelID     int64  `gorm:"column:label_id"`
	DisplayName string `gorm:"column:display_name"`
	Spaced      bool   `gorm:"column:has_spaced_slash"`
	Kind        int16  `gorm:"column:kind"`
	WorkCount   int64  `gorm:"column:work_count"`
	VNDBName    string `gorm:"column:vndb_name"`
	VNDBLatin   string `gorm:"column:vndb_latin"`
	VNDBAlias   string `gorm:"column:vndb_alias"`
	BangumiName string `gorm:"column:bangumi_name"`
	DLsiteMaker string `gorm:"column:dlsite_maker_id"`
	Aliases     string `gorm:"column:aliases"`
}

const queryTmpl = `
SELECT l.id AS label_id, l.display_name, l.kind,
       (l.display_name LIKE '%% / %%') AS has_spaced_slash,
       coalesce(wc.n, 0) AS work_count,
       coalesce(v.names, '') AS vndb_name,
       coalesce(v.latins, '') AS vndb_latin,
       coalesce(v.aliases, '') AS vndb_alias,
       coalesce(b.names, '') AS bangumi_name,
       coalesce(d.ids, '') AS dlsite_maker_id,
       coalesce(a.names, '') AS aliases
FROM catalog_label l
LEFT JOIN LATERAL (
    SELECT count(*) AS n FROM catalog_work_label wl
    JOIN catalog_work w ON w.id = wl.work_id AND w.deleted_at IS NULL
    WHERE wl.label_id = l.id) wc ON true
LEFT JOIN LATERAL (
    SELECT string_agg(p.name, ' | ' ORDER BY r.external_id) AS names,
           string_agg(p.latin, ' | ' ORDER BY r.external_id) AS latins,
           string_agg(p.alias, ' | ' ORDER BY r.external_id) AS aliases
    FROM catalog_external_ref r JOIN src_vndb.producers p ON p.id = r.external_id
    WHERE r.entity_type = 3 AND r.entity_id = l.id AND r.source_id = 2) v ON true
LEFT JOIN LATERAL (
    SELECT string_agg(p.name, ' | ' ORDER BY r.external_id) AS names
    FROM catalog_external_ref r JOIN src_bangumi.person p ON p.id = r.external_id::bigint
    WHERE r.entity_type = 3 AND r.entity_id = l.id AND r.source_id = 3) b ON true
LEFT JOIN LATERAL (
    SELECT string_agg(r.external_id, ' | ' ORDER BY r.external_id) AS ids
    FROM catalog_external_ref r
    WHERE r.entity_type = 3 AND r.entity_id = l.id AND r.source_id = 4) d ON true
LEFT JOIN LATERAL (
    SELECT string_agg(al.name || ' [' || al.lang || ']', ' | ' ORDER BY al.id) AS names
    FROM catalog_label_alias al WHERE al.label_id = l.id) a ON true
WHERE l.deleted_at IS NULL AND %s
ORDER BY l.id`

var patterns = map[string]string{
	"any":    `l.display_name LIKE '%/%'`,
	"spaced": `l.display_name LIKE '% / %'`,
}

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; this tool only ever SELECTs, so the live catalog is safe")
	out := flag.String("out", "", "CSV output path — REQUIRED")
	pattern := flag.String("pattern", "any", "which display names count as slash names: any | spaced")
	flag.Parse()

	if *dsn == "" || *out == "" {
		slog.Error("report-label-slash-names", "error", "--dsn and --out are both required")
		os.Exit(1)
	}
	pred, ok := patterns[*pattern]
	if !ok {
		slog.Error("report-label-slash-names", "error", "unknown --pattern (want any | spaced)", "pattern", *pattern)
		os.Exit(1)
	}
	if err := run(*dsn, *out, pred); err != nil {
		slog.Error("report-label-slash-names", "error", err)
		os.Exit(1)
	}
}

func run(dsn, out, pred string) error {
	db, err := database.OpenJob(dsn)
	if err != nil {
		return fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	var rows []row
	if err := db.Raw(fmt.Sprintf(queryTmpl, pred)).Scan(&rows).Error; err != nil {
		return fmt.Errorf("load slash-named labels: %w", err)
	}

	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"label_id", "display_name", "has_spaced_slash", "kind", "work_count",
		"vndb_name", "vndb_latin", "vndb_alias", "bangumi_name", "dlsite_maker_id",
		"egs_name (always empty: EGS staging is a separate local-only DB, not a kun_catalog src_* schema)",
		"aliases",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			strconv.FormatInt(r.LabelID, 10), r.DisplayName, strconv.FormatBool(r.Spaced),
			strconv.Itoa(int(r.Kind)), strconv.FormatInt(r.WorkCount, 10),
			r.VNDBName, r.VNDBLatin, r.VNDBAlias, r.BangumiName, r.DLsiteMaker, "", r.Aliases,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	slog.Info("report-label-slash-names done", "labels", len(rows), "out", out)
	return nil
}
