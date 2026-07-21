// Package workratings backfills catalog_work_rating rows for BODYLESS galgame
// works from the two scored sources of the ratings facet (refs/proj/58a,
// doc 51 §9.3 multi-media template):
//
//   - bangumi lane: the step-56a EXACT anchors (rule:bgm-title-year) join each
//     bodyless work to a src_bangumi.subject (a schema INSIDE the catalog DB —
//     single --dsn); subjects with score>0 land as a bangumi row: score on the
//     native 0-10 scale, rank (NULL when Bangumi's rank is 0 = unranked), and
//     vote_count = the SUM of the score_details buckets — the dump carries NO
//     total field (surveyed; the same derivation galgame_bangumi_meta.total
//     uses via bangumienrich.ratingTotal).
//   - erogamespace lane: EG EXACT work anchors on bodyless works (the
//     cmd/enrich-eg-scores anchor query with its claimed-only filter INVERTED
//     to bodyless-only) join the EG mirror's games (--eg-dsn, a separate
//     database): games with a non-NULL median land as an erogamespace row:
//     score = median on the native 0-100 scale, vote_count = count2, rank NULL
//     (EG has no rank facet).
//
// Discipline (55/57 lineage, all spec-pinned):
//   - Both DSNs are ALWAYS explicit — a bare run cannot touch a live DB.
//   - Dry-run is the default: the decided plan (per-lane counters + samples)
//     is identical in dry and apply; only *Written/*Conflict need --apply.
//   - XOR guard (§8.D): native rows only for bodyless works (site NULL/”);
//     the SQL already filters to bodyless, and the writer re-asserts it.
//   - Idempotent: ON CONFLICT (work_id, source_id) DO NOTHING (the
//     uq_catalog_work_rating key) — a second --apply writes zero, counting
//     the no-ops as conflicts.
//   - Limit/Offset window EACH lane's candidate list independently (chunking).
package workratings

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ruleTitleYear is the matched_by tag of the step-56a EXACT Bangumi anchors —
// the only anchor tier the bangumi lane reads (probable stays in the confirm
// bucket). The EG lane filters by link_kind=exact only (EG anchors carry no
// single backfill rule tag).
const ruleTitleYear = "rule:bgm-title-year"

// maxSamples caps how many per-lane example rows a run collects for logging /
// test assertions.
const maxSamples = 8

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN (catalog, which also
	// hosts src_bangumi) and EGDSN (the EG mirror) are both REQUIRED and never
	// defaulted. Limit/Offset window each lane's candidate list (0 = all).
	Apply  bool
	DSN    string
	EGDSN  string
	Limit  int
	Offset int
}

// Sample is one example planned row for dry-run logging / test assertions.
type Sample struct {
	WorkID     int64
	ExternalID int64 // bangumi subject id / EG game id
	Score      float64
	VoteCount  int
	Rank       *int
}

// Stats reports a run's outcome. The *Planned counters are the decided plan
// (identical in dry and apply); *Written/*Conflict are apply-only outcomes.
type Stats struct {
	// bangumi lane
	BgmCandidates int // exact-anchored bodyless works joined to their subject
	BgmNoScore    int // subject score<=0 (unrated) → nothing to write
	BgmPlanned    int // decided bangumi rows
	BgmWritten    int // bangumi rows inserted (apply)
	BgmConflict   int // ON CONFLICT no-ops (row already existed)

	// erogamespace lane
	EgCandidates    int // bodyless works carrying >=1 EG exact work anchor
	EgMultiAnchor   int // extra anchors collapsed (work had >1 EG exact anchor)
	EgMissingMirror int // chosen EG game id absent from the mirror
	EgNoMedian      int // mirror row with NULL median → nothing to write
	EgPlanned       int // decided erogamespace rows
	EgWritten       int // erogamespace rows inserted (apply)
	EgConflict      int // ON CONFLICT no-ops (row already existed)

	Refused int // XOR guard: claimed work refused at write time
	Errors  int

	BgmSamples []Sample
	EgSamples  []Sample
}

// Run resolves both lanes' candidates and forecasts (dry) or writes (apply)
// the rating rows. Returns a loggable Stats.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	if opts.EGDSN == "" {
		return nil, fmt.Errorf("EG mirror DSN is required (--eg-dsn); refusing to guess")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	egDB, err := openGorm(opts.EGDSN)
	if err != nil {
		return nil, fmt.Errorf("connect EG mirror db: %w", err)
	}
	if sqlDB, e := egDB.DB(); e == nil {
		defer sqlDB.Close()
	}

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	st := &Stats{}
	w := &writer{db: db, stats: st}

	if err := runBgmLane(ctx, db, w, reg, opts); err != nil {
		return nil, err
	}
	if err := runEgLane(ctx, db, egDB, w, reg, opts); err != nil {
		return nil, err
	}

	slog.Info("backfill-work-ratings done", "apply", opts.Apply,
		"bgm_candidates", st.BgmCandidates, "bgm_no_score", st.BgmNoScore,
		"bgm_planned", st.BgmPlanned, "bgm_written", st.BgmWritten, "bgm_conflict", st.BgmConflict,
		"eg_candidates", st.EgCandidates, "eg_multi_anchor", st.EgMultiAnchor,
		"eg_missing_mirror", st.EgMissingMirror, "eg_no_median", st.EgNoMedian,
		"eg_planned", st.EgPlanned, "eg_written", st.EgWritten, "eg_conflict", st.EgConflict,
		"refused_claimed", st.Refused, "errors", st.Errors)
	logSamples("bgm", st.BgmSamples)
	logSamples("eg", st.EgSamples)
	return st, nil
}

// runBgmLane decides and (apply) writes the bangumi rows.
func runBgmLane(ctx context.Context, db *gorm.DB, w *writer, reg registry, opts Opts) error {
	cands, err := loadBgmCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load bangumi candidates: %w", err)
	}
	st := w.stats
	st.BgmCandidates = len(cands)
	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.Score <= 0 { // unrated subject — never write a fake zero rating
			st.BgmNoScore++
			continue
		}
		votes := ratingTotal(c.ScoreDetails)
		var rank *int
		if c.Rank > 0 { // Bangumi rank 0 = unranked (ranks are 1-based)
			r := c.Rank
			rank = &r
		}
		st.BgmPlanned++
		collect(&st.BgmSamples, Sample{WorkID: c.WorkID, ExternalID: c.SubjectID, Score: c.Score, VoteCount: votes, Rank: rank})
		w.write(ctx, plannedRow{
			WorkID: c.WorkID, Site: c.Site, SourceID: reg.bangumiSource,
			Score: c.Score, VoteCount: votes, Rank: rank,
		}, opts.Apply, &st.BgmWritten, &st.BgmConflict)
	}
	return nil
}

// runEgLane decides and (apply) writes the erogamespace rows.
func runEgLane(ctx context.Context, db, egDB *gorm.DB, w *writer, reg registry, opts Opts) error {
	cands, err := loadEgCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load EG candidates: %w", err)
	}
	st := w.stats
	st.EgCandidates = len(cands)

	idSet := map[int64]bool{}
	for _, c := range cands {
		for _, id := range c.EgIDs {
			if id >= 0 {
				idSet[id] = true
			}
		}
	}
	mirror, err := loadEGMirror(ctx, egDB, keysOf(idSet))
	if err != nil {
		return fmt.Errorf("load EG mirror games: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		chosen := pickBest(c.EgIDs, mirror)
		if len(c.EgIDs) > 1 {
			st.EgMultiAnchor += len(c.EgIDs) - 1
		}
		eg, ok := mirror[chosen]
		if !ok {
			st.EgMissingMirror++
			continue
		}
		if eg.median == nil { // EG has no median — never write a fake zero
			st.EgNoMedian++
			continue
		}
		st.EgPlanned++
		collect(&st.EgSamples, Sample{WorkID: c.WorkID, ExternalID: chosen, Score: float64(*eg.median), VoteCount: eg.votes})
		w.write(ctx, plannedRow{
			WorkID: c.WorkID, Site: c.Site, SourceID: reg.egSource,
			Score: float64(*eg.median), VoteCount: eg.votes,
		}, opts.Apply, &st.EgWritten, &st.EgConflict)
	}
	return nil
}

// collect appends a capped Sample.
func collect(dst *[]Sample, s Sample) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, s)
}

func logSamples(lane string, samples []Sample) {
	for _, s := range samples {
		args := []any{"lane", lane, "work_id", s.WorkID, "external_id", s.ExternalID,
			"score", s.Score, "vote_count", s.VoteCount}
		if s.Rank != nil {
			args = append(args, "rank", *s.Rank)
		}
		slog.Info("backfill-work-ratings sample", args...)
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
