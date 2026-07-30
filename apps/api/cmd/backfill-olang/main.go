// backfill-olang restores catalog_work.olang across the galgame registry
// (refs/proj/144, the incident wave of 2026-07-29): the whole registry carried a
// flat 'ja', which turned the public /v1 release calendar's default ja+zh family
// gate into a global no-op and flooded both consumer sites with western VNs.
//
// Two lanes, in this order:
//   - lane V — a galgame-medium work with an EXACT VNDB work anchor takes
//     src_vndb.vn.olang verbatim (a missing mirror row or a blank olang is
//     counted and skipped, never guessed);
//   - lane W — a wiki-claimed work lane V did not cover maps
//     galgame.original_language through the catalog's wiki-locale inverse.
//
// Everything else (DLsite / eges / Bangumi cross-media) is left alone: 'ja' is
// the correct value there.
//
// Logic lives in internal/jobs/olangfix. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. The DSN is REQUIRED and never defaulted —
// the rehearsal copy locally, the live catalog only in the acceptance run. It is
// ONE database: the catalog Gold tables, the src_vndb mirror schema and the wiki
// galgame table all live there.
//
// Idempotent: only rows whose value actually changes are planned, so a second
// run reports an EMPTY transition matrix — that is the rehearsal's pass
// condition (dry → apply → dry). The job deliberately does NOT touch
// catalog_work.updated_at; the track lead runs a full reindex-catalog after the
// apply so the search documents pick the new olang up.
//
//	# dry-run: counters + the old→new transition matrix + samples
//	go run ./cmd/backfill-olang \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply: write the olang values
//	go run ./cmd/backfill-olang --apply --dsn "..."
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/olangfix"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + transition matrix + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate works (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := olangfix.Run(context.Background(), olangfix.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
	})
	if err != nil {
		slog.Error("backfill-olang", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-olang summary",
		"apply", *apply,
		"vn_candidates", st.VNCandidates,
		"vn_multi_anchor", st.VNMultiAnchor,
		"vn_missing_row", st.VNMissingRow,
		"vn_blank_olang", st.VNBlankOLang,
		"vn_planned", st.VNPlanned,
		"vn_unchanged", st.VNUnchanged,
		"wiki_candidates", st.WikiCandidates,
		"wiki_row_missing", st.WikiRowMissing,
		"wiki_junk_lang", st.WikiJunkLang,
		"wiki_planned", st.WikiPlanned,
		"wiki_unchanged", st.WikiUnchanged,
		"planned", st.Planned,
		"written", st.Written,
		"distinct_transitions", st.DistinctTransitions,
		"unknown_olangs", st.UnknownOLangs,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
