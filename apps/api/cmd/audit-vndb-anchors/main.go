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
// WHY MARK AND NOT RE-POINT. The VNDB dump carries no redirect, merge or
// tombstone table: a deleted entry has NO derivable successor, so re-pointing
// is impossible and guessing is forbidden.
//
// WHY MARK AND NOT DELETE. 189 of the affected anchors carry
// matched_by='rule:wiki-vndb-id' — the wiki asserted that id, and the row is
// the only surviving record that it did. (Until wave 161 there was a second
// reason: the importer behind that rule re-asserted deleted rows on its next
// run. That importer is gone, so deletion would now stick — which makes this
// a choice rather than a constraint, and the choice is still to keep the row.)
// Keeping it costs one nullable timestamp and buys three things a DELETE
// cannot: the provenance stays readable, the exact slot stays occupied so a
// duplicate claim on the same external id is still blocked, and the state is
// REVERSIBLE — VNDB restoring an entry clears dead_at on the next pass, where
// a deleted row would simply be gone. Only user-facing projections read the
// flag; importers and de-duplication continue to see the anchor.
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
