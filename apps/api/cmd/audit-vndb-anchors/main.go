// audit-vndb-anchors reconciles our work-level EXACT VNDB anchors against the
// live VNDB mirror (src_vndb.vn, a schema INSIDE the catalog database) and
// maintains catalog_external_ref.dead_at — the upstream-liveness axis.
//
// WHY THIS EXISTS. We anchor ~98.7% of VNDB and VNDB deletes roughly twenty
// entries a week, so the set of anchors pointing at a vndb.org page that now
// 404s grows continuously (112 on the 2026-08 production measurement). The
// public faces render those anchors as links, so every one of them is a broken
// link on a user-visible work page.
//
// WHY MARK AND NOT DELETE OR RE-POINT. The VNDB dump carries no redirect,
// merge or tombstone table: a deleted entry has NO derivable successor, so
// re-pointing is impossible and guessing is forbidden. Deleting the row does
// not work either — 189 of the affected anchors carry
// matched_by='rule:wiki-vndb-id' and are simply re-asserted by the next
// importer run, which is precisely the "deletion is not a decision" failure.
// The row therefore stays, keeps occupying its exact slot (so it still blocks
// a duplicate claim on the same external id) and keeps recording what the wiki
// asserted; only dead_at changes, and only user-facing projections read it.
//
// The audit is BIDIRECTIONAL and re-runnable:
//
//	absent upstream + dead_at IS NULL      → dead_at = now()
//	present upstream + dead_at IS NOT NULL → dead_at = NULL
//
// The clear-back direction is what makes the tool self-healing when VNDB
// restores an entry, and self-correcting when a previous run read a mirror
// that was merely stale.
//
//	# report only (default) — safe against any mirror, writes nothing
//	go run ./cmd/audit-vndb-anchors --dsn "host=127.0.0.1 user=postgres dbname=kun_catalog sslmode=disable"
//
//	# write
//	go run ./cmd/audit-vndb-anchors --dsn "..." --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED, never inferred from the environment")
	apply := flag.Bool("apply", false, "write dead_at (default: report-only dry run)")
	minMirrorRows := flag.Int64("min-mirror-rows", defaultMinMirrorRows,
		"refuse to --apply unless src_vndb.vn holds at least this many rows (mid-reload guard)")
	flag.Parse()

	if *dsn == "" {
		slog.Error("audit-vndb-anchors", "error", "--dsn is required")
		os.Exit(1)
	}
	if err := run(context.Background(), *dsn, *apply, *minMirrorRows, os.Stdout); err != nil {
		slog.Error("audit-vndb-anchors", "error", err)
		os.Exit(1)
	}
}
