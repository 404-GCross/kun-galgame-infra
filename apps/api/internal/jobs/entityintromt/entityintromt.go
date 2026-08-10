// Package entityintromt machine-translates ENTITY intros — characters, persons
// and labels (companies/brands) — into zh-Hans (refs/proj/172), extending the
// step-75 work-intro MT discipline to the three entity intro tables:
//
//   - provenance = 1 marks every machine row; the read face prefers the source
//     row for a language whenever both coexist.
//   - src_hash = sha256(chosen source text) is the re-translate trigger: an
//     unchanged source is idempotent (skip), a changed source re-translates
//     (guarded upsert).
//   - mt_model records the producing model for accountability.
//   - Fill-missing-language: an entity carrying ANY zh-Hans/zh-Hant SOURCE row
//     (provenance=0) is excluded; a machine row NEVER overwrites a source row.
//
// Unlike the work job's strictly disjoint ja/en lanes, one run picks the source
// PER ENTITY: the lowest-source_id ja row when any exists, else the lowest
// source_id en row (ja→zh is the shorter, more faithful hop; the en text is
// itself usually a translation). The prompt follows the chosen language.
//
// The LLM is reached only through the Translator seam (translate.go), so the
// write path is fully provable offline with a mock. dry (default): forecast
// counts + samples, NO LLM call, NO write. --dsn is ALWAYS explicit — a bare
// run cannot touch a live DB.
package entityintromt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
)

// maxSamples caps how many example decisions a run collects for the report.
const maxSamples = 30

// Lane keys, also the --lane vocabulary. Empty Lane in Opts = all three.
const (
	LaneCharacter = "character"
	LanePerson    = "person"
	LaneLabel     = "label"
)

// laneDef binds a lane to its intro table / id column / entity table. The
// three tables share the exact same shape, so one query template serves all.
type laneDef struct {
	key         string
	introTable  string
	idCol       string
	entityTable string
}

var lanes = []laneDef{
	{LaneCharacter, "catalog_character_intro", "character_id", "catalog_character"},
	{LanePerson, "catalog_person_intro", "person_id", "catalog_person"},
	{LaneLabel, "catalog_label_intro", "label_id", "catalog_label"},
}

// Opts configures a run.
type Opts struct {
	// DSN is REQUIRED and never defaulted — a bare run cannot touch a live DB.
	DSN string
	// Apply=false is a dry forecast (no LLM, no writes).
	Apply bool
	// Lane restricts the run to one lane ("" = all three).
	Lane string
	// Limit/Offset window each lane's candidate set (entity-id ASC — the
	// deterministic batch order; 0 = all). Offset is what lets the character
	// lane (~111k entities) run as resumable nightly batches.
	Limit  int
	Offset int
	// Delay rate-limits real gateway calls between apply writes (mock = 0).
	// With Workers > 1 it paces each worker independently.
	Delay time.Duration
	// Workers sizes the apply-mode pool (<=1 = serial). The upstream's
	// per-request latency dominates wall time.
	Workers int
}

// Sample is one example decision for the report. Src/Zh carry the FULL text in
// apply mode (the quality gate pastes source/translation pairs); Zh is empty in
// dry mode (no LLM called).
type Sample struct {
	Lane     string
	EntityID int64
	Decision string
	SrcLang  string
	Src      string
	Zh       string
	MTModel  string
	// Gloss is the term list injected into this candidate's prompt — the
	// quality gate needs to see WHY a name came out the way it did.
	Gloss Glossary
}

// LaneStats reports one lane's outcome. Would* are the DECIDED plan (identical
// in dry and apply); Inserted/Retranslated are rows actually written (apply
// only); Refused counts the never-overwrite guard firing (expected 0).
type LaneStats struct {
	Lane             string
	Candidates       int // entities in the windowed candidate set
	WithGlossary     int // candidates carrying at least one glossary term
	FromJa           int // candidates whose chosen source is ja
	FromEn           int // candidates whose chosen source is en
	WouldInsert      int // no machine zh row yet → new machine translation
	WouldRetranslate int // machine row exists, source hash changed → re-translate
	SkipUnchanged    int // machine row exists, source hash unchanged → idempotent skip
	Inserted         int // machine rows inserted (apply)
	Retranslated     int // machine rows re-translated (apply)
	Refused          int // guard fired: would overwrite a source row (expected 0)
	Errors           int // translate / write errors

	Samples []Sample
}

// Run resolves each selected lane's candidate set and forecasts (dry) or
// translates + writes (apply) the machine zh-Hans rows. tr is the LLM seam
// (nil is allowed in dry mode — no call is made).
func Run(ctx context.Context, tr Translator, opts Opts) ([]*LaneStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	if opts.Apply && tr == nil {
		return nil, fmt.Errorf("apply mode needs a translator")
	}
	selected, err := selectLanes(opts.Lane)
	if err != nil {
		return nil, err
	}
	db, err := database.OpenJob(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	var out []*LaneStats
	for _, lane := range selected {
		cands, err := loadCandidates(ctx, db, lane, opts.Limit, opts.Offset)
		if err != nil {
			return nil, fmt.Errorf("load %s candidates: %w", lane.key, err)
		}
		// The glossary is ALWAYS-ON and zero-config: it is loaded in dry mode
		// too, because it participates in src_hash and therefore in the forecast.
		if err := attachGlossaries(ctx, db, lane, cands); err != nil {
			return nil, fmt.Errorf("load %s glossaries: %w", lane.key, err)
		}
		st := &LaneStats{Lane: lane.key, Candidates: len(cands)}
		for _, c := range cands {
			if c.SrcLang == string(SourceJa) {
				st.FromJa++
			} else {
				st.FromEn++
			}
			if len(c.Gloss) > 0 {
				st.WithGlossary++
			}
		}
		slog.Info("entity-intro-mt candidates", "lane", lane.key,
			"candidates", len(cands), "from_ja", st.FromJa, "from_en", st.FromEn,
			"with_glossary", st.WithGlossary,
			"apply", opts.Apply, "limit", opts.Limit, "offset", opts.Offset)

		r := &runner{db: db, tr: tr, lane: lane, stats: st}
		r.process(ctx, cands, opts.Apply, opts.Delay, opts.Workers)
		if err := r.touch(ctx); err != nil {
			return nil, fmt.Errorf("touch %s entities: %w", lane.key, err)
		}
		slog.Info("entity-intro-mt lane done", "lane", lane.key, "apply", opts.Apply,
			"candidates", st.Candidates, "with_glossary", st.WithGlossary, "would_insert", st.WouldInsert,
			"would_retranslate", st.WouldRetranslate, "skip_unchanged", st.SkipUnchanged,
			"inserted", st.Inserted, "retranslated", st.Retranslated,
			"refused", st.Refused, "errors", st.Errors)
		out = append(out, st)
		if ctx.Err() != nil {
			break
		}
	}
	return out, nil
}

func selectLanes(key string) ([]laneDef, error) {
	if key == "" {
		return lanes, nil
	}
	for _, l := range lanes {
		if l.key == key {
			return []laneDef{l}, nil
		}
	}
	return nil, fmt.Errorf("unknown lane %q (want %q, %q or %q)", key, LaneCharacter, LanePerson, LaneLabel)
}
