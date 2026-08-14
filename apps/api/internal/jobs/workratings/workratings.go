package workratings

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

const ruleTitleYear = "rule:bgm-title-year"

const maxSamples = 8

type Opts struct {
	Apply     bool
	DSN       string
	EGDSN     string
	DlsiteDSN string
	Limit     int
	Offset    int
}

type Sample struct {
	WorkID     int64
	ExternalID int64
	Workno     string
	Score      float64
	VoteCount  int
	Rank       *int
}

type Stats struct {
	BgmCandidates   int
	BgmNoScore      int
	BgmPlanned      int
	BgmWritten      int
	BgmUnchanged    int
	BgmDistribution int

	EgCandidates    int
	EgMultiAnchor   int
	EgMissingMirror int
	EgNoMedian      int
	EgPlanned       int
	EgWritten       int
	EgUnchanged     int
	EgStats         int

	DlCandidates      int
	DlMissingMirror   int
	DlNoRating        int
	DlRatingPlanned   int
	DlRatingWritten   int
	DlRatingUnchanged int
	DlDistribution    int
	PopPlanned        int
	PopWritten        int
	PopUnchanged      int

	Errors int

	BgmSamples []Sample
	EgSamples  []Sample
	DlSamples  []Sample
}

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
		"bgm_distribution", st.BgmDistribution,
		"eg_candidates", st.EgCandidates, "eg_multi_anchor", st.EgMultiAnchor,
		"eg_missing_mirror", st.EgMissingMirror, "eg_no_median", st.EgNoMedian,
		"eg_planned", st.EgPlanned, "eg_written", st.EgWritten, "eg_unchanged", st.EgUnchanged,
		"eg_stats", st.EgStats,
		"dl_candidates", st.DlCandidates, "dl_missing_mirror", st.DlMissingMirror,
		"dl_no_rating", st.DlNoRating, "dl_rating_planned", st.DlRatingPlanned,
		"dl_rating_written", st.DlRatingWritten, "dl_rating_unchanged", st.DlRatingUnchanged,
		"dl_distribution", st.DlDistribution,
		"pop_planned", st.PopPlanned, "pop_written", st.PopWritten, "pop_unchanged", st.PopUnchanged,
		"errors", st.Errors)
	logSamples("bgm", st.BgmSamples)
	logSamples("eg", st.EgSamples)
	logSamples("dlsite", st.DlSamples)
	return st, nil
}

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
		if c.Score <= 0 {
			st.BgmNoScore++
			continue
		}
		buckets := bgmBuckets(c.ScoreDetails)
		votes := bucketsTotal(buckets)
		dist := marshalBuckets(buckets)
		if dist != nil {
			st.BgmDistribution++
		}
		var rank *int
		if c.Rank > 0 {
			r := c.Rank
			rank = &r
		}
		st.BgmPlanned++
		collect(&st.BgmSamples, Sample{WorkID: c.WorkID, ExternalID: c.SubjectID, Score: c.Score, VoteCount: votes, Rank: rank})
		w.write(ctx, plannedRow{
			WorkID: c.WorkID, SourceID: reg.bangumiSource,
			Score: c.Score, VoteCount: votes, Rank: rank, Distribution: dist,
		}, opts.Apply, &st.BgmWritten, &st.BgmUnchanged)
	}
	return nil
}

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
		if eg.median == nil {
			st.EgNoMedian++
			continue
		}
		stats := marshalStats(eg.stats)
		if stats != nil {
			st.EgStats++
		}
		st.EgPlanned++
		collect(&st.EgSamples, Sample{WorkID: c.WorkID, ExternalID: chosen, Score: float64(*eg.median), VoteCount: eg.votes})
		w.write(ctx, plannedRow{
			WorkID: c.WorkID, SourceID: reg.egSource,
			Score: float64(*eg.median), VoteCount: eg.votes, Stats: stats,
		}, opts.Apply, &st.EgWritten, &st.EgUnchanged)
	}
	return nil
}

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
