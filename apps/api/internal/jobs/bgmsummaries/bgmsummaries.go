package bgmsummaries

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
	Apply      bool
	DSN        string
	Limit      int
	Offset     int
	Population string
}

type Sample struct {
	WorkID    int64
	SubjectID int64
	Lang      string
	Preview   string
}

type Stats struct {
	Candidates     int
	NoSummary      int
	NoLang         int
	SkipDupLang    int
	ZhNew          int
	JaFill         int
	ZhWritten      int
	JaWritten      int
	ClaimedWritten int
	Conflict       int
	Errors         int

	ZhSamples     []Sample
	JaSamples     []Sample
	DupSamples    []Sample
	NoLangSamples []Sample
}

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
	cands, err := loadCandidates(ctx, db, reg, opts.Population, opts.Limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	workIDs := make([]int64, len(cands))
	for i, c := range cands {
		workIDs[i] = c.WorkID
	}
	exist, err := preloadExistingLangs(ctx, db, workIDs)
	if err != nil {
		return nil, fmt.Errorf("preload existing intro langs: %w", err)
	}
	slog.Info("bgm-summaries candidates", "candidates", len(cands), "apply", opts.Apply,
		"population", opts.Population, "offset", opts.Offset, "limit", opts.Limit)

	r := &runner{db: db, sourceID: reg.bangumiSource, exist: exist, stats: &Stats{Candidates: len(cands)}}
	r.process(ctx, cands, opts.Apply)
	if err := r.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	st := r.stats
	slog.Info("bgm-summaries done", "apply", opts.Apply, "population", opts.Population,
		"candidates", st.Candidates, "no_summary", st.NoSummary, "no_lang", st.NoLang,
		"skip_dup_lang", st.SkipDupLang,
		"zh_new", st.ZhNew, "ja_fill", st.JaFill,
		"zh_written", st.ZhWritten, "ja_written", st.JaWritten,
		"claimed_written", st.ClaimedWritten,
		"conflict", st.Conflict, "errors", st.Errors)
	logSamples("zh_new", st.ZhSamples)
	logSamples("ja_fill", st.JaSamples)
	logSamples("skip_dup_lang", st.DupSamples)
	logSamples("no_lang", st.NoLangSamples)
	return st, nil
}

func logSamples(category string, samples []Sample) {
	for _, s := range samples {
		slog.Info("bgm-summaries sample", "category", category,
			"work_id", s.WorkID, "subject_id", s.SubjectID, "lang", s.Lang, "preview", s.Preview)
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
