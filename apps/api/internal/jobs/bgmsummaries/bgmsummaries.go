// Package bgmsummaries enriches galgame catalog works with Bangumi summaries
// (BGM enrichment wave, refs/proj/57; population + language rework in 166).
// Step 56a/56c anchored works to Bangumi subjects (exact);
// src_bangumi.subject.summary is already in the catalog DB (dump-ingested — no
// API, no token, no bytes). This step lands those summaries as
// catalog_work_intro rows under source=bangumi. Zero schema, zero openapi —
// pure DB, the step-55 intro path.
//
// Discipline (spec-pinned):
//   - EVERY EXACT anchor (matched_by UNRESTRICTED — the 66/69/71 ruling that
//     each exact tier asserts identity); probable anchors sit in the confirm
//     bucket and are never touched.
//   - Population: bodyless AND claimed (refs/proj/166). The bodyless-only gate
//     this job was born with encoded the read-time intro BRIDGE, which W1-pre
//     (refs/proj/140) replaced with catalog_work_intro for every work; keeping
//     the gate after that just left published works blank. See sitePredicate.
//   - Lang detection: hiragana-against-han share, with a no-CJK abstention.
//     See detectLang — the old "any kana ⇒ ja" rule mis-filed ~3.5k Chinese
//     summaries. The vocabulary MUST match the read face (ja/en/zh-Hans/
//     zh-Hant) — the 55/56 lesson.
//   - Fill-missing-language: a row is written ONLY when the work has NO intro
//     row in the detected language yet (ANY source). One rule covers both the
//     zh new-language writes and the ja gap-fills, and naturally skips the
//     ja summaries duplicating an existing DLsite ja intro (noise guard: the
//     read face should never surface two same-language intros for one work).
//     It is also what keeps this job off the curated first-party text: a
//     claimed work's own intro already occupies its language.
//   - Text verbatim except CRLF→LF (the dump carries \r\n artifacts; verified:
//     every \r in the type=4 summaries is part of a \r\n pair).
//   - Idempotent: fill-missing itself is idempotent (a written row makes the
//     lang present, so a re-run skips); ON CONFLICT (work_id,lang,source_id)
//     DO NOTHING is the write-time backstop. A second --apply writes zero.
//   - --dsn is ALWAYS explicit — a bare run cannot touch a live DB.
package bgmsummaries

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ruleTitleYear is the matched_by tag of the step-56a EXACT anchors. The
// candidate query no longer filters on it (every exact tier is admitted); it
// remains a representative exact-anchor rule the tests seed with.
const ruleTitleYear = "rule:bgm-title-year"

// maxSamples caps how many per-category example rows a run collects for
// logging / test assertions.
const maxSamples = 8

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN is REQUIRED and never
	// defaulted — a bare run cannot touch a live DB (the 55 discipline).
	// Limit/Offset window the candidate works for chunked runs (0 = all).
	Apply  bool
	DSN    string
	Limit  int
	Offset int
	// Population selects the works this run may write for (all|bodyless|
	// claimed; empty = all). A staged rollout runs claimed alone; the resident
	// schedule wants all.
	Population string
}

// Sample is one example decision for dry-run logging / test assertions.
type Sample struct {
	WorkID    int64
	SubjectID int64
	Lang      string
	Preview   string // first runes of the normalized summary, newlines flattened
}

// Stats reports a run's outcome. ZhNew/JaFill are the DECIDED plan (identical
// in dry and apply); *Written are the rows actually inserted (apply only);
// Conflict counts ON CONFLICT no-ops (the backstop firing).
type Stats struct {
	Candidates     int // exact-anchored works in the population, joined to their subject
	NoSummary      int // subject summary empty/whitespace → nothing to write
	NoLang         int // summary evidences no CJK → abstain rather than guess a tag
	SkipDupLang    int // work already has an intro row in the detected lang (any source)
	ZhNew          int // decided zh-Hans writes (work had no zh-Hans intro)
	JaFill         int // decided ja gap-fills (work had no ja intro)
	ZhWritten      int // zh-Hans rows inserted (apply)
	JaWritten      int // ja rows inserted (apply)
	ClaimedWritten int // of the inserted rows, how many landed on a claimed work
	Conflict       int // ON CONFLICT said the row already existed
	Errors         int

	ZhSamples     []Sample
	JaSamples     []Sample
	DupSamples    []Sample
	NoLangSamples []Sample
}

// Run resolves the candidate set and forecasts (dry) or writes (apply) the
// fill-missing-language intro rows. Returns a loggable Stats.
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
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
