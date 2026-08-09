package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// sourceKeyVndb is the catalog_source registry key of the VNDB source. The
// numeric id is looked up, never hard-coded — registry ids are data.
const sourceKeyVndb = "vndb"

// defaultMinMirrorRows is the MANDATORY mid-reload guardrail.
//
// src_vndb is rebuildable staging: cmd/ingest-vndb replaces src_vndb.vn
// wholesale from the VNDB dump, so between the truncate and the end of the
// COPY the table legitimately holds anything from zero rows upward. An audit
// run landing inside that window would see ~64k anchors as "absent upstream"
// and mark EVERY VNDB anchor dead in one transaction, stripping every VNDB
// link from the site. That is the failure this number exists to prevent, and
// it is why the guard is a refusal rather than a warning.
//
// The threshold is derived from the real table: src_vndb.vn holds ~65,300 rows
// (local 2026-08 snapshot), and VNDB's catalogue only grows in the long run —
// its ~20 deletions a week are far outrun by new entries. 50,000 sits ~23%
// below today's count: comfortably below any plausible real dump for years,
// while a partial load is overwhelmingly likely to be caught (a mid-COPY table
// spends almost all of its life under this line). It is a flag so an operator
// facing a genuinely smaller future dump can lower it deliberately, on the
// record, instead of the tool silently doing the wrong thing.
//
// DOES THE GUARD ALSO PROTECT THE CLEAR-BACK DIRECTION, e.g. against a mirror
// that is complete but STALE? Partly, and deliberately so. A row count cannot
// detect staleness at all — an old full dump passes any count threshold. What
// saves us is that the two directions have wildly asymmetric blast radii:
//
//   - mark-dead against an INCOMPLETE mirror is catastrophic and wholesale
//     (all ~64k anchors at once), which is what the count threshold catches;
//   - clear-back against a STALE mirror can at worst resurrect the handful of
//     anchors deleted upstream since that dump, i.e. it re-exposes a few links
//     that already 404 today — the pre-audit status quo, no worse — and the
//     next run on a fresh mirror re-marks them.
//
// So one count gate on the whole apply is the right shape: both directions are
// behind it, staleness is reported (the mirror's ingested_at is printed on
// every run) rather than guessed at, and the tool never refuses to run for a
// risk whose worst case is the state it was written to improve.
const defaultMinMirrorRows int64 = 50_000

// stats is one audit run's outcome.
type stats struct {
	Anchors     int64 // work-level exact VNDB anchors examined
	MirrorRows  int64 // rows in src_vndb.vn
	MirrorFresh *time.Time
	ToMark      int64 // absent upstream, dead_at IS NULL
	ToClear     int64 // present upstream, dead_at IS NOT NULL
	Marked      int64
	Cleared     int64
}

func run(ctx context.Context, dsn string, apply bool, minMirrorRows int64, w io.Writer) error {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	st, err := audit(ctx, db, apply, minMirrorRows)
	if err != nil {
		return err
	}
	report(w, st, apply)
	return nil
}

// audit measures both directions and, with apply, writes them.
func audit(ctx context.Context, db *gorm.DB, apply bool, minMirrorRows int64) (stats, error) {
	var st stats
	db = db.WithContext(ctx)

	var srcID int16
	if err := db.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKeyVndb).Scan(&srcID).Error; err != nil {
		return st, fmt.Errorf("look up vndb source id: %w", err)
	}
	if srcID == 0 {
		return st, fmt.Errorf("catalog_source has no %q row", sourceKeyVndb)
	}

	if err := db.Raw(`SELECT count(*) FROM src_vndb.vn`).Scan(&st.MirrorRows).Error; err != nil {
		return st, fmt.Errorf("count src_vndb.vn (is the VNDB mirror loaded?): %w", err)
	}
	if err := db.Raw(`SELECT max(ingested_at) FROM src_vndb.vn`).Scan(&st.MirrorFresh).Error; err != nil {
		return st, fmt.Errorf("read src_vndb.vn ingested_at: %w", err)
	}

	if err := db.Raw(`SELECT count(*) FROM catalog_external_ref
		WHERE source_id = ? AND entity_type = ? AND link_kind = ?`,
		srcID, model.EntityTypeWork, model.LinkKindExact).Scan(&st.Anchors).Error; err != nil {
		return st, fmt.Errorf("count anchors: %w", err)
	}

	// external_id and src_vndb.vn.id share the "v"-prefixed spelling verbatim,
	// so the comparison is a direct equality join with no transform.
	const markPredicate = `source_id = ? AND entity_type = ? AND link_kind = ? AND dead_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM src_vndb.vn v WHERE v.id = catalog_external_ref.external_id)`
	const clearPredicate = `source_id = ? AND entity_type = ? AND link_kind = ? AND dead_at IS NOT NULL
		AND EXISTS (SELECT 1 FROM src_vndb.vn v WHERE v.id = catalog_external_ref.external_id)`
	args := []any{srcID, model.EntityTypeWork, model.LinkKindExact}

	if err := db.Raw(`SELECT count(*) FROM catalog_external_ref WHERE `+markPredicate, args...).
		Scan(&st.ToMark).Error; err != nil {
		return st, fmt.Errorf("count anchors absent upstream: %w", err)
	}
	if err := db.Raw(`SELECT count(*) FROM catalog_external_ref WHERE `+clearPredicate, args...).
		Scan(&st.ToClear).Error; err != nil {
		return st, fmt.Errorf("count revived anchors: %w", err)
	}
	if !apply {
		return st, nil
	}
	if st.MirrorRows < minMirrorRows {
		return st, fmt.Errorf(
			"refusing to apply: src_vndb.vn holds %d rows, below the --min-mirror-rows floor of %d — "+
				"the mirror looks partially loaded, and applying against it would mark live anchors dead wholesale",
			st.MirrorRows, minMirrorRows)
	}

	// One transaction for both directions: an audit run is a single statement
	// about the mirror it read, and half of it is not a coherent state.
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE catalog_external_ref SET dead_at = now() WHERE `+markPredicate, args...)
		if res.Error != nil {
			return fmt.Errorf("mark dead: %w", res.Error)
		}
		st.Marked = res.RowsAffected
		res = tx.Exec(`UPDATE catalog_external_ref SET dead_at = NULL WHERE `+clearPredicate, args...)
		if res.Error != nil {
			return fmt.Errorf("clear revived: %w", res.Error)
		}
		st.Cleared = res.RowsAffected
		return nil
	})
	return st, err
}

func report(w io.Writer, st stats, apply bool) {
	fresh := "unknown (mirror empty)"
	if st.MirrorFresh != nil {
		fresh = st.MirrorFresh.Format(time.RFC3339)
	}
	fmt.Fprintf(w, "[audit-vndb-anchors] mirror src_vndb.vn rows=%d ingested_at=%s\n", st.MirrorRows, fresh)
	fmt.Fprintf(w, "[audit-vndb-anchors] work-level exact vndb anchors=%d\n", st.Anchors)
	if apply {
		fmt.Fprintf(w, "[audit-vndb-anchors] marked dead=%d cleared (revived)=%d\n", st.Marked, st.Cleared)
		return
	}
	fmt.Fprintf(w, "[audit-vndb-anchors] would mark dead=%d would clear (revived)=%d (dry run — pass --apply to write)\n",
		st.ToMark, st.ToClear)
}
