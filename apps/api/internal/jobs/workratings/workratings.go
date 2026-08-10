// Package workratings backfills catalog_work_rating (and, for the DLsite lane,
// catalog_work_popularity) rows for BODYLESS galgame works from the scored
// sources of the ratings facet (refs/proj/58a + 62, doc 51 §9.3 multi-media
// template):
//
//   - bangumi lane: EVERY EXACT Bangumi work anchor (matched_by unrestricted —
//     the 66/69/71 ruling) joins each work to a src_bangumi.subject (a
//     schema INSIDE the catalog DB — single --dsn); subjects with score>0 land
//     as a bangumi row: score on the native 0-10 scale, rank (NULL when
//     Bangumi's rank is 0 = unranked), and vote_count = the SUM of the
//     score_details buckets — the dump carries NO total field (surveyed; the
//     same derivation galgame_bangumi_meta.total uses via
//     bangumienrich.ratingTotal).
//   - erogamespace lane: EG EXACT work anchors (the cmd/enrich-eg-scores anchor
//     query with its claim filter removed) join the EG mirror's games
//     (--eg-dsn, a separate
//     database): games with a non-NULL median land as an erogamespace row:
//     score = median on the native 0-100 scale, vote_count = count2, rank NULL
//     (EG has no rank facet).
//   - dlsite lane (step 62): DLsite EXACT RELEASE anchors on bodyless
//     GALGAME-medium works (workno anchors are SKU-natured, doc 17 R3; the
//     ASMR family also carries DLsite anchors and is out of scope by ruling)
//     join the DLsite mirror's works (--dlsite-dsn, a separate database),
//     values extracted from info_json (surveyed: the only JSON column carrying
//     the counters). Rated works land as a dlsite RATING row: score =
//     rate_average_2dp on the native 0-5 star scale (the wire key
//     rate_average_star is that value half-star-bucketed ×10 — a widget
//     encoding, not stored), vote_count = rate_count, rank NULL. Published
//     counters land as POPULARITY rows (catalog_work_popularity, one per
//     metric): dl_count→downloads, wishlist_count→wishlist,
//     review_count→reviews; absent/negative counters never become rows.
//
// Discipline (55/57 lineage, all spec-pinned):
//   - Every DSN is ALWAYS explicit — a bare run cannot touch a live DB.
//   - Dry-run is the default: the decided plan (per-lane counters + samples)
//     is identical in dry and apply; only *Written/*Unchanged need --apply.
//   - EVERY galgame work, claimed or bodyless (W1-pre bridge nativization,
//     refs/proj/140 §2.6). The step-88 site predicate and the write-time XOR
//     guard are gone with the read-time bridge they protected: the catalog face
//     no longer reads a claimed work's bgm/dlsite/eg scores out of the wiki meta
//     tables, so this importer is their single persistent writer everywhere.
//   - Refresh-runnable (step 62 upsert unification): every write is
//     ON CONFLICT DO UPDATE with change detection — a re-run after a mirror
//     refresh updates rows in place; a re-run against unchanged staging is a
//     no-op (RowsAffected 0 counts as *Unchanged*). This replaced 58a's
//     DO NOTHING: ratings/popularity are volatile, so the bgm/eg lanes became
//     refreshable for free.
//   - Limit/Offset window EACH lane's candidate list independently (chunking).
package workratings

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

// ruleTitleYear is the matched_by tag of the step-56a EXACT Bangumi anchors.
// The bangumi lane no longer filters on it (every exact tier is admitted, like
// the EG/DLsite lanes which filter by link_kind=exact only); it remains a
// representative exact-anchor rule the tests seed with.
const ruleTitleYear = "rule:bgm-title-year"

// maxSamples caps how many per-lane example rows a run collects for logging /
// test assertions.
const maxSamples = 8

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN (catalog, which also
	// hosts src_bangumi), EGDSN (the EG mirror) and DlsiteDSN (the DLsite
	// mirror) are ALL REQUIRED and never defaulted. Limit/Offset window each
	// lane's candidate list (0 = all).
	Apply     bool
	DSN       string
	EGDSN     string
	DlsiteDSN string
	Limit     int
	Offset    int
}

// Sample is one example planned rating row for dry-run logging / test
// assertions. ExternalID carries the numeric source id lanes (bangumi subject /
// EG game); Workno the DLsite lane's product id.
type Sample struct {
	WorkID     int64
	ExternalID int64
	Workno     string
	Score      float64
	VoteCount  int
	Rank       *int
}

// Stats reports a run's outcome. The *Planned counters are the decided plan
// (identical in dry and apply); *Written (rows inserted or value-updated) and
// *Unchanged (change-detected no-ops) are apply-only outcomes.
type Stats struct {
	// bangumi lane
	BgmCandidates int // exact-anchored bodyless works joined to their subject
	BgmNoScore    int // subject score<=0 (unrated) → nothing to write
	BgmPlanned    int // decided bangumi rows
	BgmWritten    int // bangumi rows inserted or updated (apply)
	BgmUnchanged  int // change-detected no-ops (row already current)

	// erogamespace lane
	EgCandidates    int // bodyless works carrying >=1 EG exact work anchor
	EgMultiAnchor   int // extra anchors collapsed (work had >1 EG exact anchor)
	EgMissingMirror int // chosen EG game id absent from the mirror
	EgNoMedian      int // mirror row with NULL median → nothing to write
	EgPlanned       int // decided erogamespace rows
	EgWritten       int // erogamespace rows inserted or updated (apply)
	EgUnchanged     int // change-detected no-ops (row already current)

	// dlsite lane (ratings + popularity from one candidate pass)
	DlCandidates      int // bodyless galgame-medium works carrying a DLsite exact release anchor
	DlMissingMirror   int // anchored workno absent from the mirror
	DlNoRating        int // mirror row without a published rating → no rating row (popularity may still land)
	DlRatingPlanned   int // decided dlsite rating rows
	DlRatingWritten   int // dlsite rating rows inserted or updated (apply)
	DlRatingUnchanged int // change-detected no-ops (row already current)
	PopPlanned        int // decided popularity rows (all metrics)
	PopWritten        int // popularity rows inserted or updated (apply)
	PopUnchanged      int // change-detected no-ops (row already current)

	Errors int

	BgmSamples []Sample
	EgSamples  []Sample
	DlSamples  []Sample
}

// Run resolves the lanes' candidates and forecasts (dry) or writes (apply)
// the rating + popularity rows. Returns a loggable Stats.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	if opts.EGDSN == "" {
		return nil, fmt.Errorf("EG mirror DSN is required (--eg-dsn); refusing to guess")
	}
	if opts.DlsiteDSN == "" {
		return nil, fmt.Errorf("DLsite mirror DSN is required (--dlsite-dsn); refusing to guess")
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
	dlsiteDB, err := openGorm(opts.DlsiteDSN)
	if err != nil {
		return nil, fmt.Errorf("connect DLsite mirror db: %w", err)
	}
	if sqlDB, e := dlsiteDB.DB(); e == nil {
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
	if err := runDlsiteLane(ctx, db, dlsiteDB, w, reg, opts); err != nil {
		return nil, err
	}
	if err := w.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	slog.Info("backfill-work-ratings done", "apply", opts.Apply,
		"bgm_candidates", st.BgmCandidates, "bgm_no_score", st.BgmNoScore,
		"bgm_planned", st.BgmPlanned, "bgm_written", st.BgmWritten, "bgm_unchanged", st.BgmUnchanged,
		"eg_candidates", st.EgCandidates, "eg_multi_anchor", st.EgMultiAnchor,
		"eg_missing_mirror", st.EgMissingMirror, "eg_no_median", st.EgNoMedian,
		"eg_planned", st.EgPlanned, "eg_written", st.EgWritten, "eg_unchanged", st.EgUnchanged,
		"dl_candidates", st.DlCandidates, "dl_missing_mirror", st.DlMissingMirror,
		"dl_no_rating", st.DlNoRating, "dl_rating_planned", st.DlRatingPlanned,
		"dl_rating_written", st.DlRatingWritten, "dl_rating_unchanged", st.DlRatingUnchanged,
		"pop_planned", st.PopPlanned, "pop_written", st.PopWritten, "pop_unchanged", st.PopUnchanged,
		"errors", st.Errors)
	logSamples("bgm", st.BgmSamples)
	logSamples("eg", st.EgSamples)
	logSamples("dlsite", st.DlSamples)
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
			WorkID: c.WorkID, SourceID: reg.bangumiSource,
			Score: c.Score, VoteCount: votes, Rank: rank,
		}, opts.Apply, &st.BgmWritten, &st.BgmUnchanged)
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
			WorkID: c.WorkID, SourceID: reg.egSource,
			Score: float64(*eg.median), VoteCount: eg.votes,
		}, opts.Apply, &st.EgWritten, &st.EgUnchanged)
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
		args := []any{"lane", lane, "work_id", s.WorkID,
			"score", s.Score, "vote_count", s.VoteCount}
		if s.Workno != "" {
			args = append(args, "workno", s.Workno)
		} else {
			args = append(args, "external_id", s.ExternalID)
		}
		if s.Rank != nil {
			args = append(args, "rank", *s.Rank)
		}
		slog.Info("backfill-work-ratings sample", args...)
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
