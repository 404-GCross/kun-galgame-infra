// report-label-slash-names is a READ-ONLY reconnaissance tool for the labels
// whose display_name is a slash-separated LIST of brands rather than one name
// (refs/proj/175 wave A, deliverable 4). They come in verbatim from the DLsite
// maker field — a maker page may front a whole brand family, e.g. label 5960:
//
//	インターハート / Candy Soft / ぐみそふと / はちみつそふと / REAL / ...
//
// The tool WRITES NOTHING. It emits one CSV row per affected label with every
// canonical-name candidate its anchors can offer, so a human can decide, per
// label, whether the right heal is "pick the first brand", "split into several
// labels", or "leave it". Nothing here proposes an answer.
//
// Candidate columns, one per anchored source:
//
//	vndb_name / vndb_latin / vndb_alias  src_vndb.producers, via the
//	                                     entity_type=3 source_id=2 anchor
//	bangumi_name                         src_bangumi.person, via source_id=3
//	dlsite_maker_id                      the source_id=4 anchor's external id —
//	                                     the id, not a name: DLsite is where the
//	                                     slash string came from, so it can only
//	                                     say WHICH maker page, never a cleaner name
//	aliases                              what catalog_label_alias already carries
//	egs_name                             ALWAYS EMPTY — see below
//
// egs_name is a deliberately empty column. The ErogameScape staging mirror is a
// LOCAL-ONLY database of its own, not a src_* schema inside kun_catalog, and
// this tool runs where kun_catalog's src_* schemas live. The column is kept so
// a later pass can fill it without changing the CSV's shape.
//
// A label with several anchors of one source contributes all of them, joined by
// " | ", ordered by external id — the tool never picks for you.
//
// SELECTION. `--pattern any` (default) takes every display_name containing a
// slash (127 live labels on the 2026-07-30 local snapshot); `--pattern spaced`
// narrows to the ' / ' spelling (22). has_spaced_slash is a column either way,
// so one report covers both readings.
//
//	go run ./cmd/report-label-slash-names \
//	    --dsn "host=/run/postgresql user=postgres dbname=kun_catalog sslmode=disable" \
//	    --out /tmp/label-slash-names.csv
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

// row is one affected label with its candidate names. Every candidate column is
// a pre-joined string: the CSV is for human eyes, not for a re-parse.
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

// query resolves the affected labels and left-joins every candidate source.
// LEFT JOIN throughout: a label with no vndb anchor is still a report row —
// "no candidate anywhere" is one of the answers the reader needs.
//
// The %s is the display_name predicate, chosen from a fixed pair below; nothing
// user-supplied is ever interpolated.
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

// patterns are the two readings of "a slash name", both fixed literals.
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
	// The egs_name header carries its own explanation — the column is empty by
	// construction and a reader must not read that as "EGS knows nothing".
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
