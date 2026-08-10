// Package charattrs backfills the typical-set character attribute columns
// (birthday month/day, blood type, height/weight, BWH, cup, gender) plus the
// Bangumi long-tail extra on catalog_character, from the in-DB staging schemas
// (field PR C2, refs/proj/81). Characters are catalog-NATIVE entities — no
// claimed/bodyless split, no XOR — so every EXACT-anchored character is a
// candidate. Two lanes, VNDB first:
//
//   - vndb: src_vndb.chars typed columns (birthday mmdd, bloodt, sex, cup_size,
//     height/weight/s_bust/s_waist/s_hip). Sentinel decode (0/unknown/""→drop);
//     matched_by unrestricted (link_kind=exact only — the 66/69/71/79 judgement).
//   - bgm:  src_bangumi.character.infobox_parsed. Promotion keys (生日/血型/身高/
//     体重/BWH/性别) → typed columns via the parse.go regexes; a year, an
//     out-of-range number, or an abnormal birthday keeps its raw string in
//     extra. The long tail (星座/年龄/属性/趣味 …) folds into extra under the
//     "bgm" namespace; identity/alias/citation/VA keys are excluded (carried by
//     the alias / credits waves).
//
// Survivorship (refs/proj/81): where both sources carry a field, VNDB wins
// (typed column > regex over free text); Bangumi fills the gap. Every real-column
// write is recorded in field_provenance (R8 array, latest first). Overwrite
// rule: an empty column is written; a non-empty column is rewritten only when
// its latest writer is a pipeline source of equal-or-lower priority (a source
// re-parsing itself, or vndb overriding bgm) — a human ("user") edit is never
// touched. The bgm extra namespace is replaced wholesale each run. A second
// --apply writes zero.
//
// --dsn is ALWAYS explicit — a bare run cannot touch a live DB.
package charattrs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

// Lane keys (also the --only vocabulary).
const (
	LaneVNDB    = "vndb"
	LaneBangumi = "bgm"
)

const maxSamples = 8

// Opts configures a run. Apply=false is a dry-run forecast. DSN is REQUIRED.
type Opts struct {
	Apply  bool
	DSN    string
	Limit  int
	Offset int
	Only   string // "" = both lanes; "vndb" | "bgm"
}

// sample is one decided example for dry-run logging / test assertions.
type sample struct {
	EntityID int64
	Cols     []string
}

// LaneStats reports one lane's outcome. The *Written counters are the DECIDED
// plan (identical in dry and apply); RowsUpdated counts rows that received (or
// would receive) an UPDATE.
type LaneStats struct {
	Candidates      int
	NoProposal      int // source decoded to nothing writable (all sentinel/empty)
	RowsUpdated     int
	GenderWritten   int
	BirthdayWritten int
	BloodWritten    int
	HeightWritten   int
	WeightWritten   int
	BustWritten     int
	WaistWritten    int
	HipWritten      int
	CupWritten      int
	ExtraRows       int // bgm: chars carrying a non-empty long-tail namespace
	ExtraChanged    int // bgm: chars whose extra namespace was (re)written
	ExtraKeyHits    int // bgm: total long-tail key occurrences
	OutOfRange      int // bgm: values dropped for range (raw kept in extra)
	Truncated       int // bgm: extra values truncated at 2KB
	Errors          int
	Samples         []sample
}

// Stats is the whole run.
type Stats struct {
	VNDB    LaneStats
	Bangumi LaneStats
}

// Run resolves each lane's candidates and forecasts (dry) or writes (apply) the
// attribute columns + extra. VNDB runs first so its typed values own their
// fields before the Bangumi lane fills the gaps.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	switch opts.Only {
	case "", LaneVNDB, LaneBangumi:
	default:
		return nil, fmt.Errorf("unknown --only lane %q (want %s|%s)", opts.Only, LaneVNDB, LaneBangumi)
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
	now := time.Now().UTC().Format(time.RFC3339)
	st := &Stats{}
	if opts.Only == "" || opts.Only == LaneVNDB {
		if err := runVNDBLane(ctx, db, reg, opts, now, st); err != nil {
			return nil, err
		}
	}
	if opts.Only == "" || opts.Only == LaneBangumi {
		if err := runBGMLane(ctx, db, reg, opts, now, st); err != nil {
			return nil, err
		}
	}
	logLane(LaneVNDB, &st.VNDB, opts.Apply)
	logLane(LaneBangumi, &st.Bangumi, opts.Apply)
	return st, nil
}

func runVNDBLane(ctx context.Context, db *gorm.DB, reg registry, opts Opts, now string, st *Stats) error {
	cands, err := loadVNDBCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return err
	}
	st.VNDB.Candidates = len(cands)
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.EntityID
	}
	states, err := preloadStates(ctx, db, ids)
	if err != nil {
		return err
	}
	slog.Info("charattrs vndb candidates", "n", len(cands), "apply", opts.Apply)
	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		writeChar(ctx, db, sourceVNDB, now, states[c.EntityID], c.decode(), nil, &st.VNDB, opts.Apply)
	}
	return nil
}

func runBGMLane(ctx context.Context, db *gorm.DB, reg registry, opts Opts, now string, st *Stats) error {
	cands, err := loadBGMCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return err
	}
	st.Bangumi.Candidates = len(cands)
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.EntityID
	}
	states, err := preloadStates(ctx, db, ids)
	if err != nil {
		return err
	}
	slog.Info("charattrs bgm candidates", "n", len(cands), "apply", opts.Apply)
	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res := parseBGMInfobox(c.Infobox)
		st.Bangumi.OutOfRange += res.outOfRange
		st.Bangumi.Truncated += res.truncated
		st.Bangumi.ExtraKeyHits += len(res.extra)
		if len(res.extra) > 0 {
			st.Bangumi.ExtraRows++
		}
		writeChar(ctx, db, sourceBangumi, now, states[c.EntityID], res.attrs, res.extra, &st.Bangumi, opts.Apply)
	}
	return nil
}

// writeChar plans one character's column + extra updates, records the counters,
// and (in apply mode) flushes the single UPDATE.
func writeChar(ctx context.Context, db *gorm.DB, source, now string, cs charState, prop attrs, extraBGM map[string]any, stats *LaneStats, apply bool) {
	hasProposal := prop.any() || len(extraBGM) > 0
	if !hasProposal {
		stats.NoProposal++
		return
	}
	w := newCharWriter(source, now, cs.FieldProvenance)
	var cols []string
	plan := func(col string, ok bool, counter *int) {
		if ok {
			*counter++
			cols = append(cols, col)
		}
	}
	bMonth := w.i16("birthday_month", cs.Month, prop.month)
	bDay := w.i16("birthday_day", cs.Day, prop.day)
	if bMonth || bDay {
		stats.BirthdayWritten++
		cols = append(cols, "birthday")
	}
	plan("blood_type", w.i16("blood_type", cs.Blood, prop.blood), &stats.BloodWritten)
	plan("height_cm", w.i16("height_cm", cs.Height, prop.height), &stats.HeightWritten)
	plan("weight_kg", w.i16("weight_kg", cs.Weight, prop.weight), &stats.WeightWritten)
	plan("bust_cm", w.i16("bust_cm", cs.Bust, prop.bust), &stats.BustWritten)
	plan("waist_cm", w.i16("waist_cm", cs.Waist, prop.waist), &stats.WaistWritten)
	plan("hip_cm", w.i16("hip_cm", cs.Hip, prop.hip), &stats.HipWritten)
	plan("cup", w.str("cup", cs.Cup, prop.cup), &stats.CupWritten)
	plan("gender", w.i16("gender", cs.Gender, prop.gender), &stats.GenderWritten)

	if extraBGM != nil {
		if w.setExtraNamespace(cs.Extra, extraNamespaceBGM, extraBGM) {
			stats.ExtraChanged++
			cols = append(cols, "extra")
		}
	}
	if len(w.updates) == 0 {
		return
	}
	stats.RowsUpdated++
	if len(stats.Samples) < maxSamples {
		stats.Samples = append(stats.Samples, sample{EntityID: cs.ID, Cols: cols})
	}
	if !apply {
		return
	}
	if err := w.flush(ctx, db, cs.ID); err != nil {
		stats.Errors++
		slog.Warn("charattrs write", "entity", cs.ID, "source", source, "err", err)
	}
}

func logLane(lane string, s *LaneStats, apply bool) {
	slog.Info("charattrs lane done", "lane", lane, "apply", apply,
		"candidates", s.Candidates, "no_proposal", s.NoProposal, "rows_updated", s.RowsUpdated,
		"gender", s.GenderWritten, "birthday", s.BirthdayWritten, "blood", s.BloodWritten,
		"height", s.HeightWritten, "weight", s.WeightWritten,
		"bust", s.BustWritten, "waist", s.WaistWritten, "hip", s.HipWritten, "cup", s.CupWritten,
		"extra_rows", s.ExtraRows, "extra_changed", s.ExtraChanged, "extra_key_hits", s.ExtraKeyHits,
		"out_of_range", s.OutOfRange, "truncated", s.Truncated, "errors", s.Errors)
}

// any reports whether the proposal has at least one non-nil field.
func (a attrs) any() bool {
	return a.month != nil || a.day != nil || a.blood != nil || a.height != nil ||
		a.weight != nil || a.bust != nil || a.waist != nil || a.hip != nil ||
		a.cup != nil || a.gender != nil
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
