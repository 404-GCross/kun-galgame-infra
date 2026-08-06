// Package intromt machine-translates work intros ja→zh-Hans over a selected
// population lane: bodyless catalog-native works (the doc-75 pilot) or claimed
// wiki-face works (the W1-pre zh refill — see Population). It fills a MISSING
// language only: a work that already carries any zh-Hans/zh-Hant SOURCE row
// (provenance=0) is skipped, and a machine row NEVER overwrites a source row. Three hard
// constraints back the "machine text never masquerades as source data" rule:
//
//   - provenance = 1 marks every machine row; the read face prefers the source
//     row for a language whenever both coexist.
//   - src_hash = sha256(source ja text) is the re-translate trigger: an
//     unchanged source is idempotent (skip), a changed source re-translates
//     (guarded upsert).
//   - mt_model records the producing model for accountability.
//
// The LLM is reached only through the Translator seam (translate.go), so the
// write path is fully provable offline with a mock — the pilot's rehearsal
// runs full --apply against a mock translator; real translations happen only
// for the human quality gate (--limit N --apply against a live gateway).
//
// dry (default): forecast counts + samples, NO LLM call, NO write.
// --apply: translate (insert / re-translate) + write; --limit caps to the most
// popular N; --dsn is ALWAYS explicit (a bare run cannot touch a live DB).
package intromt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// maxSamples caps how many example decisions a run collects for the report.
const maxSamples = 30

// Opts configures a run.
type Opts struct {
	// DSN is REQUIRED and never defaulted — a bare run cannot touch a live DB.
	DSN string
	// Apply=false is a dry forecast (no LLM, no writes).
	Apply bool
	// Population selects the candidate lane; empty defaults to
	// PopulationBodyless (the pilot lane, backward compatible).
	Population Population
	// SourceLang selects the language translated FROM; empty defaults to
	// SourceJa (the pilot lane, backward compatible). See SourceEn for why the
	// English lane is a strictly disjoint last resort.
	SourceLang SourceLang
	// Top caps the popularity-ranked candidate population. The CLI defaults it
	// to 5000 (the pilot ceiling); 0 means UNLIMITED — the refs/proj/173
	// full-sweep posture, an explicit choice rather than a fallback. Limit then
	// windows to the most-popular N for a sample run (0 = all within Top).
	Top   int
	Limit int
	// Delay rate-limits real gateway calls between apply writes (mock = 0).
	// With Workers > 1 it paces each worker independently.
	Delay time.Duration
	// Workers sizes the apply-mode pool (<=1 = serial, the default). The
	// upstream's per-request latency dominates wall time — 8 workers is still
	// ~10 req/min against Workers AI's 300 req/min class limits.
	Workers int
}

// Sample is one example decision for the report. Ja/Zh carry the FULL text in
// apply mode (the quality gate pastes source/translation pairs); Zh is empty in
// dry mode (no LLM called).
type Sample struct {
	WorkID   int64
	Decision string
	Ja       string
	Zh       string
	MTModel  string
	// Gloss is the term list injected into this candidate's prompt — the
	// quality gate needs to see WHY a name came out the way it did.
	Gloss Glossary
}

// Stats reports a run's outcome. Would* are the DECIDED plan (identical in dry
// and apply); Inserted/Retranslated are rows actually written (apply only);
// Refused counts the never-overwrite guard firing (expected 0).
type Stats struct {
	Candidates       int // works in the popularity-ranked set (post Top/Limit)
	WithGlossary     int // candidates carrying at least one glossary term
	WouldInsert      int // no zh row yet → new machine translation
	WouldRetranslate int // machine row exists, source hash changed → re-translate
	SkipUnchanged    int // machine row exists, source hash unchanged → idempotent skip
	Inserted         int // machine rows inserted (apply)
	Retranslated     int // machine rows re-translated (apply)
	Refused          int // guard fired: would overwrite a source row (expected 0)
	Errors           int // translate / write errors

	Samples []Sample
}

// Run resolves the candidate set and forecasts (dry) or translates + writes
// (apply) the machine intro rows. tr is the LLM seam (nil is allowed in dry
// mode — no call is made). Returns a loggable Stats.
func Run(ctx context.Context, tr Translator, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	if opts.Apply && tr == nil {
		return nil, fmt.Errorf("apply mode needs a translator")
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
	pop := opts.Population
	if pop == "" {
		pop = PopulationBodyless
	}
	src := opts.SourceLang
	if src == "" {
		src = SourceJa
	}
	cands, err := loadCandidates(ctx, db, reg, pop, src, opts.Top, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	// The glossary is ALWAYS-ON and zero-config: it is loaded in dry mode too,
	// because it participates in src_hash and therefore in the forecast.
	if err := attachGlossaries(ctx, db, cands); err != nil {
		return nil, fmt.Errorf("load glossaries: %w", err)
	}
	withGloss := 0
	for _, c := range cands {
		if len(c.Gloss) > 0 {
			withGloss++
		}
	}
	slog.Info("intro-mt candidates", "population", pop, "candidates", len(cands),
		"with_glossary", withGloss, "apply", opts.Apply, "top", opts.Top, "limit", opts.Limit)

	r := &runner{db: db, tr: tr, stats: &Stats{Candidates: len(cands), WithGlossary: withGloss}}
	r.process(ctx, cands, opts.Apply, opts.Delay, opts.Workers)
	if err := r.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	st := r.stats
	slog.Info("intro-mt done", "apply", opts.Apply,
		"candidates", st.Candidates, "with_glossary", st.WithGlossary, "would_insert", st.WouldInsert,
		"would_retranslate", st.WouldRetranslate, "skip_unchanged", st.SkipUnchanged,
		"inserted", st.Inserted, "retranslated", st.Retranslated,
		"refused", st.Refused, "errors", st.Errors)
	return st, nil
}

// beginSample starts a capped Sample (ja text captured now; zh filled on
// apply). Returns the sample's INDEX, -1 when the cap is reached — an index
// stays valid across append reallocations, which a slice-element pointer would
// not under the concurrent pool.
func (r *runner) beginSample(c candidate, dec decision) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stats.Samples) >= maxSamples {
		return -1
	}
	r.stats.Samples = append(r.stats.Samples, Sample{
		WorkID: c.WorkID, Decision: decisionName(dec), Ja: c.JaText, Gloss: c.Gloss,
	})
	return len(r.stats.Samples) - 1
}

func (r *runner) finishSample(i int, zh, mtModel string) {
	if i < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats.Samples[i].Zh = zh
	r.stats.Samples[i].MTModel = mtModel
}

func decisionName(d decision) string {
	switch d {
	case decInsert:
		return "insert"
	case decRetrans:
		return "retranslate"
	default:
		return "skip_unchanged"
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
