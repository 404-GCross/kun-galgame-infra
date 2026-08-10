package entityintros

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

const (
	LaneCharBangumi   = "char-bgm"
	LaneCharVNDB      = "char-vndb"
	LaneCharEG        = "char-eg"
	LanePersonBangumi = "person-bgm"
)

const maxSamples = 8

type Opts struct {
	Apply  bool
	DSN    string
	Limit  int
	Offset int
	Only   string
	EGDSN  string
}

type Sample struct {
	EntityID   int64
	ExternalID string
	Lang       string
	Preview    string
}

type LaneStats struct {
	Candidates      int
	NoSupply        int
	NoText          int
	SpoilerStripped int
	SkipDupLang     int
	JaNew           int
	ZhNew           int
	EnNew           int
	JaWritten       int
	ZhWritten       int
	EnWritten       int
	Conflict        int
	Errors          int
	Touched         int

	Samples    []Sample
	DupSamples []Sample
}

type Stats struct {
	CharBangumi   LaneStats
	CharVNDB      LaneStats
	CharEG        LaneStats
	PersonBangumi LaneStats
}

func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	switch opts.Only {
	case "", LaneCharBangumi, LaneCharVNDB, LaneCharEG, LanePersonBangumi:
	default:
		return nil, fmt.Errorf("unknown --only lane %q (want %s|%s|%s|%s)",
			opts.Only, LaneCharBangumi, LaneCharVNDB, LaneCharEG, LanePersonBangumi)
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	var egDB *gorm.DB
	if opts.Only == "" || opts.Only == LaneCharEG {
		if opts.EGDSN == "" {
			return nil, fmt.Errorf("the %s lane needs the erogamespace DSN (--eg-dsn); refusing to guess", LaneCharEG)
		}
		if egDB, err = openGorm(opts.EGDSN); err != nil {
			return nil, fmt.Errorf("connect erogamespace db: %w", err)
		}
		if sqlDB, e := egDB.DB(); e == nil {
			defer sqlDB.Close()
		}
	}

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	st := &Stats{}
	if err := runCharacterLanes(ctx, db, egDB, reg, opts, st); err != nil {
		return nil, err
	}
	if err := runPersonLane(ctx, db, reg, opts, st); err != nil {
		return nil, err
	}
	logLane(LaneCharBangumi, &st.CharBangumi, opts.Apply)
	logLane(LaneCharVNDB, &st.CharVNDB, opts.Apply)
	logLane(LaneCharEG, &st.CharEG, opts.Apply)
	logLane(LanePersonBangumi, &st.PersonBangumi, opts.Apply)
	return st, nil
}

func logLane(lane string, st *LaneStats, apply bool) {
	slog.Info("entity-intros lane done", "lane", lane, "apply", apply,
		"candidates", st.Candidates, "no_supply", st.NoSupply, "no_text", st.NoText,
		"spoiler_stripped", st.SpoilerStripped, "skip_dup_lang", st.SkipDupLang,
		"ja_new", st.JaNew, "zh_new", st.ZhNew, "en_new", st.EnNew,
		"ja_written", st.JaWritten, "zh_written", st.ZhWritten, "en_written", st.EnWritten,
		"conflict", st.Conflict, "errors", st.Errors, "touched_works", st.Touched)
	for _, s := range st.Samples {
		slog.Info("entity-intros sample", "lane", lane, "category", "new",
			"entity_id", s.EntityID, "external_id", s.ExternalID, "lang", s.Lang, "preview", s.Preview)
	}
	for _, s := range st.DupSamples {
		slog.Info("entity-intros sample", "lane", lane, "category", "skip_dup_lang",
			"entity_id", s.EntityID, "external_id", s.ExternalID, "lang", s.Lang, "preview", s.Preview)
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
