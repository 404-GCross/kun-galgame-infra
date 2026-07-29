// Package entityintros backfills catalog_character_intro and
// catalog_person_intro from the in-DB staging schemas (field PR C1,
// refs/proj/65). Characters and persons are catalog-NATIVE entities — no
// claimed/bodyless split, no XOR, no bridging — so unlike the work-intro waves
// every anchored entity is a candidate. Four lanes, one run:
//
//   - char-bgm:    character anchors (entity_type=4, source=bangumi, exact) →
//     src_bangumi.character.summary; lang by the step-57 kana heuristic
//     (ja / zh-Hans).
//   - char-vndb:   character anchors (entity_type=4, source=vndb, exact) →
//     src_vndb.chars.description; lang=en always (VNDB writes descriptions in
//     English). [spoiler]…[/spoiler] spans are removed ENTIRELY — the unified
//     read shape has no spoiler flag, and leaking unmarked spoilers is
//     dishonest (the 58b spoiler_level=0 reasoning); light markup ([url=],
//     [b], …) is unwrapped, tags dropped, text kept.
//   - char-eg:     character anchors (entity_type=4, source=erogamespace, exact)
//     → the erogamespace mirror's appearances.raw->>'formal_explanation';
//     lang=ja always (EG writes Japanese). Spoiler write-ups (netabare) are
//     excluded, and a character appearing in several games contributes its
//     longest text. erogamespace is a SEPARATE DATABASE, so this lane takes a
//     second DSN and joins anchor→text in Go (refs/proj/120).
//   - person-bgm:  person anchors (entity_type=0, source=bangumi, exact) →
//     src_bangumi.person.summary. Person anchors are EMPTY today (person
//     identity resolution is deferred; persons carry no external refs yet) —
//     the lane's value is the pipeline: re-runs auto-expand once anchors land.
//     Credit-NAME anchors are deliberately never used as a person proxy.
//
// Discipline (spec-pinned, inherited from step 57):
//   - Fill-missing-language: a row is written ONLY when the entity has NO
//     intro row in the detected language yet (ANY source).
//   - Text verbatim except CRLF→LF; the VNDB lane additionally strips spoiler
//     spans / unwraps markup and trims the leftover whitespace removal leaves.
//   - No upsert: intros are non-volatile (unlike the step-62 popularity
//     counters, which change on every mirror refresh and need
//     ON CONFLICT DO UPDATE) — a dump refresh is handled by re-running
//     fill-missing, which only ADDS languages that are still absent.
//   - Idempotent: fill-missing itself re-run-safe; ON CONFLICT
//     (entity_id,lang,source_id) DO NOTHING is the write-time backstop. A
//     second --apply writes zero — and therefore bumps no updated_at either.
//   - --dsn is ALWAYS explicit — a bare run cannot touch a live DB.
package entityintros

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Lane keys, also the --only vocabulary.
const (
	LaneCharBangumi   = "char-bgm"
	LaneCharVNDB      = "char-vndb"
	LaneCharEG        = "char-eg"
	LanePersonBangumi = "person-bgm"
)

// maxSamples caps how many per-category example rows a run collects for
// logging / test assertions.
const maxSamples = 8

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN is REQUIRED and never
	// defaulted — a bare run cannot touch a live DB (the 55/57 discipline).
	// Limit/Offset window each lane's candidate entities independently
	// (0 = all). Only restricts the run to one lane ("" = all four).
	Apply  bool
	DSN    string
	Limit  int
	Offset int
	Only   string
	// EGDSN reaches the erogamespace mirror — a separate DATABASE, unlike the
	// src_bangumi / src_vndb schemas the other lanes read through DSN. Required
	// whenever char-eg runs; the caller derives the default (the catalog server
	// with dbname=erogamespace), this package never guesses one.
	EGDSN string
}

// Sample is one example decision for dry-run logging / test assertions.
type Sample struct {
	EntityID   int64
	ExternalID string
	Lang       string
	Preview    string // first runes of the prepared text, newlines flattened
}

// LaneStats reports one lane's outcome. The *New counters are the DECIDED plan
// (identical in dry and apply); *Written are rows actually inserted (apply
// only); Conflict counts ON CONFLICT no-ops (the backstop firing).
type LaneStats struct {
	Candidates      int // anchored live entities joined to their staging row
	NoSupply        int // char-eg: anchored entity the source has no usable text for
	NoText          int // staging text empty/whitespace after preparation
	SpoilerStripped int // vndb lane: texts that had ≥1 [spoiler] span removed
	SkipDupLang     int // entity already has an intro row in the lang (any source)
	JaNew           int // decided ja writes (bangumi + eg lanes)
	ZhNew           int // decided zh-Hans writes (bangumi lanes)
	EnNew           int // decided en writes (vndb lane)
	JaWritten       int
	ZhWritten       int
	EnWritten       int
	Conflict        int
	Errors          int
	Touched         int // host works whose updated_at this lane bumped (character lanes; person-bgm has none)

	Samples    []Sample
	DupSamples []Sample
}

// Stats is the whole run: one LaneStats per lane.
type Stats struct {
	CharBangumi   LaneStats
	CharVNDB      LaneStats
	CharEG        LaneStats
	PersonBangumi LaneStats
}

// Run resolves each lane's candidate set and forecasts (dry) or writes (apply)
// the fill-missing-language intro rows. Returns a loggable Stats.
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

	// The EG mirror is only opened when its lane runs, so a --only run on the
	// in-DB lanes needs no erogamespace access at all.
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
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
