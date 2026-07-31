// Package olangfix restores catalog_work.olang across the galgame registry
// (refs/proj/144, the incident wave of 2026-07-29).
//
// ── what was broken ──────────────────────────────────────────────────────────
//
// Every galgame row in the registry carried a flat 'ja': no lane had ever
// written a real original language, they all stamped the placeholder. That made
// the public /v1 release calendar's default ja+zh family gate (service.PublicOLang)
// a GLOBAL NO-OP — 20,437 works whose true olang is en-us, 2,136 ru and the rest
// poured into both consumer sites' calendars. The 2026-07 month bucket measured
// 165 works where the wiki-era JP/CN plane held ~66.
//
// ── the two supply lanes ─────────────────────────────────────────────────────
//
//   - Lane V (the authority): a galgame-medium work carrying an EXACT VNDB WORK
//     anchor takes src_vndb.vn.olang verbatim. external_id is 'v19658' and
//     src_vndb.vn.id is the same text, so the anchor joins the mirror directly —
//     no prefix surgery. A missing mirror row or a blank olang is COUNTED and
//     skipped, never guessed at.
//   - Lane W (the remainder): a work claimed by the galgame wiki that Lane V did
//     not cover maps galgame.original_language through model.MapWikiOLang (the
//     exact inverse of the VNDB→wiki mapping the wiki sync itself applies).
//
// Every other work — DLsite / eges / Bangumi-cross-media minted, no VNDB anchor,
// no wiki claim — is left alone: 'ja' is the correct value for those catalogues,
// and importer/bgmtype4gated already writes a true olang where it knows better.
//
// ── why this job does NOT touch updated_at ───────────────────────────────────
//
// 轨长裁定 2026-07-29. Every other facet backfill in this tree touches its host
// work so the /v1/catalog/changes keyset feed republishes it. This one must not:
// olang is a POPULATION PREDICATE, not work content, and bumping 82k watermarks
// would shove the entire registry through the changes feed and invalidate every
// downstream ETag at once. The search documents do carry olang, so the track lead
// runs a full reindex-catalog after the apply — that, not the feed, is how the
// new values reach the read faces.
//
// ── discipline (worktags / dlsitegenres lineage) ─────────────────────────────
//
//   - The DSN is ALWAYS explicit — a bare run cannot touch a live database.
//   - Dry-run is the default: the decided plan (counters, the old→new transition
//     matrix, samples) is identical in dry and apply; only Written needs --apply.
//   - Idempotent: only rows whose value actually CHANGES are planned, and the
//     UPDATE re-asserts `olang IS DISTINCT FROM` as a concurrency backstop. A
//     second run plans nothing, so its transition matrix is empty — that is the
//     rehearsal's pass condition.
//   - Limit/Offset window the combined candidate list (chunking).
package olangfix

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Lane names, carried on every candidate and sample so a report says which
// supply decided a row.
const (
	laneVNDB = "vndb"
	laneWiki = "wiki"
)

// maxSamples caps the example rows a run collects; maxTransitions caps the
// old→new digest.
const (
	maxSamples     = 8
	maxTransitions = 20
)

// Opts configures a run. Apply=false is a dry-run forecast (no writes). DSN is
// REQUIRED and never defaulted; it points at ONE database holding the catalog
// Gold tables, the src_vndb mirror schema and the wiki galgame table — the
// single-DSN shape production deploys.
type Opts struct {
	Apply  bool
	DSN    string
	Limit  int
	Offset int
}

// Sample is one example planned change for logging / test assertions.
type Sample struct {
	WorkID int64
	Lane   string
	From   string
	To     string
	// Source is what decided it: the VNDB id for lane V, the raw wiki
	// original_language for lane W.
	Source string
}

// Transition is one cell of the old→new transition matrix: how many works move
// From→To. Only CHANGES are counted, so an all-zero (empty) matrix is exactly
// the "nothing left to do" signal a re-run must produce.
type Transition struct {
	From  string
	To    string
	Works int
}

// Stats reports a run's outcome. Every counter except Written is identical in
// dry and apply. The two *Candidates counters size the whole POPULATION; every
// other counter describes the rows this run's Limit/Offset window actually
// decided, so a chunked run reports its own slice against the full denominator.
type Stats struct {
	VNCandidates  int // galgame works carrying an exact VNDB work anchor
	VNMultiAnchor int // works with >1 such anchor (lowest external_id wins)
	VNMissingRow  int // anchored id absent from the src_vndb mirror → skipped
	VNBlankOLang  int // mirror row present but olang blank → skipped
	VNPlanned     int // lane V rows whose olang changes
	VNUnchanged   int // lane V rows already correct

	WikiCandidates int // wiki-claimed works Lane V did not cover
	WikiRowMissing int // claimed work whose galgame row is gone → OLangDefault
	WikiJunkLang   int // ''/NULL/'others'/'ck' → OLangDefault (counted, not hidden)
	WikiPlanned    int
	WikiUnchanged  int

	// CuratedOverride counts candidates skipped because a human edited
	// catalog.work.olang through the editing engine (03 定案 §0 line 2). Their
	// value is not a placeholder this job may correct — it is a decision.
	CuratedOverride int

	Planned int // VNPlanned + WikiPlanned
	Written int // rows actually updated (apply)
	Errors  int

	DistinctTransitions int          // distinct old→new pairs across the plan
	Transitions         []Transition // the biggest cells, capped
	// UnknownOLangs are planned values absent from the src_vndb.vn vocabulary —
	// a WARNING only (olang is an open vocabulary), never a blocker.
	UnknownOLangs []string
	Samples       []Sample
}

// Run resolves the candidates and forecasts (dry) or writes (apply) the olang
// values. Returns a loggable Stats.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	st := &Stats{}
	cands, err := loadCandidates(ctx, db, reg, st)
	if err != nil {
		return nil, err
	}
	vocab, err := loadOLangVocabulary(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("load src_vndb olang vocabulary: %w", err)
	}

	cands = window(cands, opts.Limit, opts.Offset)

	// CURATED OVERRIDE: preload, in one query, the works whose olang a human has
	// already edited. Loading the whole window's set up front rather than asking
	// per row is not an optimization detail — a per-row EXISTS turns an 82k-row
	// job into 82k round trips.
	workIDs := make([]int64, 0, len(cands))
	for _, c := range cands {
		workIDs = append(workIDs, c.WorkID)
	}
	edited, err := editing.EditedEntities(ctx, db, editspec.TypeWork, editspec.FieldWorkOLang, workIDs)
	if err != nil {
		return nil, fmt.Errorf("load curated olang overrides: %w", err)
	}

	matrix := map[Transition]int{}
	unknown := map[string]bool{}
	w := &writer{db: db, stats: st}

	for _, c := range cands {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if edited[c.WorkID] {
			st.CuratedOverride++
			continue
		}
		next, ok := decide(c, st)
		if !ok {
			continue
		}
		if next == c.OldOLang {
			if c.Lane == laneVNDB {
				st.VNUnchanged++
			} else {
				st.WikiUnchanged++
			}
			continue
		}
		if c.Lane == laneVNDB {
			st.VNPlanned++
		} else {
			st.WikiPlanned++
		}
		st.Planned++
		matrix[Transition{From: c.OldOLang, To: next}]++
		if !vocab[next] {
			unknown[next] = true
		}
		if len(st.Samples) < maxSamples {
			st.Samples = append(st.Samples, Sample{
				WorkID: c.WorkID, Lane: c.Lane, From: c.OldOLang, To: next, Source: c.source(),
			})
		}
		w.plan(c.WorkID, next, opts.Apply)
	}
	if err := w.flush(ctx, opts.Apply); err != nil {
		return nil, fmt.Errorf("write olang: %w", err)
	}

	st.DistinctTransitions = len(matrix)
	st.Transitions = topTransitions(matrix)
	st.UnknownOLangs = sortedKeys(unknown)

	slog.Info("backfill-olang done", "apply", opts.Apply,
		"vn_candidates", st.VNCandidates, "vn_multi_anchor", st.VNMultiAnchor,
		"vn_missing_row", st.VNMissingRow, "vn_blank_olang", st.VNBlankOLang,
		"curated_override", st.CuratedOverride,
		"vn_planned", st.VNPlanned, "vn_unchanged", st.VNUnchanged,
		"wiki_candidates", st.WikiCandidates, "wiki_row_missing", st.WikiRowMissing,
		"wiki_junk_lang", st.WikiJunkLang, "wiki_planned", st.WikiPlanned,
		"wiki_unchanged", st.WikiUnchanged,
		"planned", st.Planned, "written", st.Written, "errors", st.Errors,
		"distinct_transitions", st.DistinctTransitions)
	for _, tr := range st.Transitions {
		slog.Info("backfill-olang transition", "from", tr.From, "to", tr.To, "works", tr.Works)
	}
	for _, v := range st.UnknownOLangs {
		slog.Warn("backfill-olang olang outside the VNDB vocabulary", "olang", v)
	}
	for _, s := range st.Samples {
		slog.Info("backfill-olang sample", "work_id", s.WorkID, "lane", s.Lane,
			"from", s.From, "to", s.To, "source", s.Source)
	}
	return st, nil
}

// decide resolves a candidate's target olang, bumping the skip counters. ok=false
// means the row carries no usable signal and must be left exactly as it is —
// only lane V can reach that verdict, because lane W always has OLangDefault to
// fall back on.
func decide(c candidate, st *Stats) (string, bool) {
	if c.Lane == laneVNDB {
		if !c.VNFound {
			st.VNMissingRow++
			return "", false
		}
		if c.VNOLang == "" {
			st.VNBlankOLang++
			return "", false
		}
		return c.VNOLang, true
	}
	if !c.WikiFound {
		// The claim points at a galgame row that no longer exists. Nothing to map;
		// the deliberate default is the honest answer.
		st.WikiRowMissing++
		return model.OLangDefault, true
	}
	olang, ok := model.MapWikiOLang(c.WikiLang)
	if !ok {
		st.WikiJunkLang++
	}
	return olang, true
}

// topTransitions digests the matrix into its biggest cells (ties broken by
// from/to for determinism), capped at maxTransitions.
func topTransitions(matrix map[Transition]int) []Transition {
	out := make([]Transition, 0, len(matrix))
	for tr, n := range matrix {
		tr.Works = n
		out = append(out, tr)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Works != out[j].Works {
			return out[i].Works > out[j].Works
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	if len(out) > maxTransitions {
		out = out[:maxTransitions]
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
