// backfill-character-instances projects the VNDB cross-universe variant links
// (src_vndb.chars.main — "this char is an INSTANCE of that one, and the link
// itself may be a spoiler (main_spoil)") onto catalog_character.instance_of
// (doc 17 R6; refs/proj/106). The column was born with the C2 wave but never
// filled; the S2S read face (characters.instance_of) and the dedup guard
// (catalog-dedup-batch vndb-instance detox) both consume it.
//
// Idempotent change-detected UPDATE: instance_of is (re)pointed at the CURRENT
// exact-anchor holder of the main char id, so a re-run after a character merge
// heals stale pointers (execute-day checklist item). A vndb char with no main
// claim never touches its catalog row.
//
//	go run ./cmd/backfill-character-instances --dsn "..."           # dry
//	go run ./cmd/backfill-character-instances --dsn "..." --apply
package main

import (
	"flag"
	"log/slog"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// pairJoin is the resolved (instance, main) projection: both ends carry a live
// EXACT vndb char anchor. Self-loops (both ids converged post-merge) drop.
const pairJoin = `
	FROM catalog_external_ref ri
	JOIN catalog_source s ON s.id = ri.source_id AND s.key = 'vndb'
	JOIN src_vndb.chars ch ON ch.id = ri.external_id AND ch.main <> ''
	JOIN catalog_external_ref rm ON rm.source_id = ri.source_id AND rm.entity_type = 4
		AND rm.link_kind = 0 AND rm.external_id = ch.main AND rm.entity_id <> ri.entity_id
	JOIN catalog_character c ON c.id = ri.entity_id AND c.deleted_at IS NULL
	JOIN catalog_character m ON m.id = rm.entity_id AND m.deleted_at IS NULL
	WHERE ri.entity_type = 4 AND ri.link_kind = 0`

func main() {
	dsn := flag.String("dsn", "", "catalog DSN (also hosts src_vndb) — REQUIRED")
	apply := flag.Bool("apply", false, "write (default: dry-run counters only)")
	flag.Parse()
	if *dsn == "" {
		slog.Error("--dsn is required; refusing to guess the target database")
		os.Exit(1)
	}
	db, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		slog.Error("connect", "error", err)
		os.Exit(1)
	}

	var linksTotal int64
	if err := db.Raw(`SELECT count(*) FROM src_vndb.chars WHERE main <> ''`).Scan(&linksTotal).Error; err != nil {
		slog.Error("count links", "error", err)
		os.Exit(1)
	}
	var st struct {
		Pairs       int64 `gorm:"column:pairs"`
		WouldChange int64 `gorm:"column:would_change"`
		AlreadyOK   int64 `gorm:"column:already_ok"`
	}
	if err := db.Raw(`SELECT count(*) AS pairs,
		count(*) FILTER (WHERE c.instance_of IS DISTINCT FROM rm.entity_id) AS would_change,
		count(*) FILTER (WHERE c.instance_of = rm.entity_id) AS already_ok` + pairJoin).
		Scan(&st).Error; err != nil {
		slog.Error("count pairs", "error", err)
		os.Exit(1)
	}
	written := int64(0)
	if *apply {
		res := db.Exec(`UPDATE catalog_character c SET instance_of = j.main_id
			FROM (SELECT ri.entity_id AS inst_id, rm.entity_id AS main_id` + pairJoin + `) j
			WHERE c.id = j.inst_id AND c.instance_of IS DISTINCT FROM j.main_id`)
		if res.Error != nil {
			slog.Error("apply", "error", res.Error)
			os.Exit(1)
		}
		written = res.RowsAffected
	}
	slog.Info("backfill-character-instances done", "apply", *apply,
		"vndb_main_links", linksTotal, "pairs_resolved", st.Pairs,
		"unresolved", linksTotal-st.Pairs,
		"would_change", st.WouldChange, "already_ok", st.AlreadyOK, "written", written)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
