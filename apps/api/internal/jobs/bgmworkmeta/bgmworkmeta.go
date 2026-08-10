// Package bgmworkmeta backfills two Bangumi-side facets for galgame works in one
// candidate pass (refs/proj/71, ledger T3 — two similar BGM fields batched, zero
// schema; T2 = refs/proj/70 §3/§8, 88):
//
//   - Field A, meta_tags → catalog_work_tag (source=bangumi): the subject's
//     meta_tags (surveyed: jsonb array of strings — Bangumi's MODERATED
//     official tag vocabulary, platform/genre style, no vote counts) land one
//     row per tag with Count=0. Count=0 distinguishes a moderated meta tag
//     from the voted 58b folksonomy rows inside the same table (the 68
//     DLsite-genre precedent: count=0 = "no vote semantics on this source").
//     A same-name collision with an existing folksonomy row hits the
//     (work_id, name, source_id) unique key → ON CONFLICT DO NOTHING keeps
//     the voted row. FILL semantics, not upsert: meta_tags are quasi-static;
//     a dump-refresh re-run only adds names that newly appeared. T2: Field A
//     admits CLAIMED works — meta_tags is a catalog-native SOURCE lane merged
//     with the wiki bridge at the read face (no write-time guard).
//
//   - Field B, favorite shelves → catalog_work_popularity (source=bangumi):
//     the subject's favorite (surveyed: jsonb object carrying exactly the
//     five collection shelves {wish, done, doing, on_hold, dropped} on every
//     game row — Bangumi's "collect" shelf is serialized as "done" in the
//     dump) lands one row per shelf under the PopularityMetricBgm* vocabulary
//     (10-14 — the step-62 extensible-metric design point exercised for the
//     first time). UPSERT (ON CONFLICT DO UPDATE, change-detected — favorites
//     are volatile, the 62 write pattern verbatim; a dump-refresh re-run
//     heals values). A present 0 is a REAL row (about a third of shelf values
//     are 0); an absent shelf writes no row. T2b (refs/proj/102): Field B
//     admits CLAIMED works too — the popularity XOR became per-source at
//     the read face (dlsite stays bridge-exclusive, bgm shelves read
//     native), so the write-time guard is retired.
//
// Candidates: galgame works (claimed OR bodyless — T2) carrying an EXACT Bangumi
// WORK anchor, matched_by UNRESTRICTED — every exact tier asserts identity (the
// 66/69 ruling; the releasemeta bgm lane is the code precedent). Both fields
// admit claimed works (Field A since T2, Field B since T2b — refs/proj/102):
// no write-time claim guard remains. src_bangumi is a schema INSIDE the
// catalog DB — a single --dsn covers the whole run.
//
// Discipline (55/57/58/62 lineage, all spec-pinned):
//   - The DSN is ALWAYS explicit — a bare run cannot touch a live DB.
//   - Dry-run is the default: the decided plan (per-field counters + samples)
//     is identical in dry and apply; only the write outcomes need --apply.
//   - Claim policy (T2 + T2b): both fields materialize for claimed and
//     bodyless works alike — no write-time guard.
//   - Idempotent: a second --apply writes zero — field A counts the no-ops as
//     conflicts (DO NOTHING), field B as unchanged (change-detected upsert).
//   - Limit/Offset window the candidate work list (chunking).
package bgmworkmeta

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

// maxSamples caps how many per-field example rows a run collects for logging /
// test assertions; maxTopNames caps the meta-tag top-frequency digest.
const (
	maxSamples  = 8
	maxTopNames = 10
)

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN (catalog, which also
	// hosts src_bangumi) is REQUIRED and never defaulted. Limit/Offset window
	// the candidate work list (0 = all).
	Apply  bool
	DSN    string
	Limit  int
	Offset int
}

// TagSample is one example planned meta-tag row (field A).
type TagSample struct {
	WorkID    int64
	SubjectID int64
	Name      string
}

// FavSample is one example planned favorite-shelf row (field B).
type FavSample struct {
	WorkID    int64
	SubjectID int64
	Bucket    string // the dump's shelf key (wish/done/doing/on_hold/dropped)
	Metric    int16  // the PopularityMetricBgm* constant it maps to
	Value     int64
}

// NameFreq is one entry of field A's top-frequency digest: how many works the
// meta-tag name is planned on.
type NameFreq struct {
	Name  string
	Works int
}

// Stats reports a run's outcome. The plan counters are identical in dry and
// apply; the write outcomes (MetaWritten/MetaConflict, FavWritten/FavUnchanged)
// are apply-only.
type Stats struct {
	Candidates int // exact-anchored galgame works (claimed + bodyless) joined to their subject

	// Field A: meta_tags → catalog_work_tag (fill, Count=0).
	MetaNoTags    int // meta_tags NULL / empty array → nothing to write
	MetaNotArray  int // meta_tags malformed (not an array of strings) → counted skip
	MetaNameBlank int // element blank after trim → skipped
	MetaDup       int // duplicate name within one subject → collapsed
	MetaPlanned   int // decided tag rows
	MetaWritten   int // rows inserted (apply)
	MetaConflict  int // ON CONFLICT no-ops — an existing folksonomy/meta row kept
	MetaDistinct  int // distinct names across the plan

	// Field B: favorite shelves → catalog_work_popularity (change-detected upsert).
	FavNoObject   int // favorite NULL / not an object / empty object → nothing to write
	FavUnknownKey int // shelf key outside the five-shelf vocabulary → counted skip
	FavBadValue   int // non-integer or negative shelf value → skipped
	FavPlanned    int // decided popularity rows
	FavWritten    int // rows inserted or value-updated (apply)
	FavUnchanged  int // change-detected no-ops (row already current)

	Errors int

	MetaTopNames []NameFreq
	MetaSamples  []TagSample
	FavSamples   []FavSample
}

// Run resolves the candidates and forecasts (dry) or writes (apply) both
// fields' rows. Returns a loggable Stats.
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
	cands, err := loadCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	st := &Stats{Candidates: len(cands)}
	w := &writer{db: db, stats: st}
	nameFreq := map[string]int{}

	for _, c := range cands {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Field A: moderated meta tags, Count=0 fill.
		for _, name := range parseMetaTags(c.MetaTags, st) {
			st.MetaPlanned++
			nameFreq[name]++
			collectTag(&st.MetaSamples, TagSample{WorkID: c.WorkID, SubjectID: c.SubjectID, Name: name})
			w.writeTag(ctx, tagRow{WorkID: c.WorkID, SourceID: reg.bangumiSource, Name: name}, opts.Apply)
		}
		// Field B: favorite shelves, change-detected upsert.
		for _, b := range parseFavorite(c.Favorite, st) {
			st.FavPlanned++
			collectFav(&st.FavSamples, FavSample{WorkID: c.WorkID, SubjectID: c.SubjectID, Bucket: b.Bucket, Metric: b.Metric, Value: b.Value})
			w.writeFavorite(ctx, favRow{WorkID: c.WorkID, SourceID: reg.bangumiSource, Metric: b.Metric, Value: b.Value}, opts.Apply)
		}
	}

	if err := w.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	st.MetaDistinct = len(nameFreq)
	st.MetaTopNames = topNames(nameFreq)

	slog.Info("backfill-bgm-work-meta done", "apply", opts.Apply,
		"candidates", st.Candidates,
		"meta_no_tags", st.MetaNoTags, "meta_not_array", st.MetaNotArray,
		"meta_name_blank", st.MetaNameBlank, "meta_dup", st.MetaDup,
		"meta_planned", st.MetaPlanned, "meta_distinct_names", st.MetaDistinct,
		"meta_written", st.MetaWritten, "meta_conflict", st.MetaConflict,
		"fav_no_object", st.FavNoObject, "fav_unknown_key", st.FavUnknownKey,
		"fav_bad_value", st.FavBadValue, "fav_planned", st.FavPlanned,
		"fav_written", st.FavWritten, "fav_unchanged", st.FavUnchanged,
		"errors", st.Errors)
	for _, nf := range st.MetaTopNames {
		slog.Info("backfill-bgm-work-meta top meta tag", "name", nf.Name, "works", nf.Works)
	}
	for _, s := range st.MetaSamples {
		slog.Info("backfill-bgm-work-meta meta sample", "work_id", s.WorkID, "subject_id", s.SubjectID, "name", s.Name)
	}
	for _, s := range st.FavSamples {
		slog.Info("backfill-bgm-work-meta favorite sample", "work_id", s.WorkID, "subject_id", s.SubjectID,
			"bucket", s.Bucket, "metric", s.Metric, "value", s.Value)
	}
	return st, nil
}

// topNames digests field A's name→work frequency map into the most-frequent
// meta-tag names (ties broken by name for determinism), capped at maxTopNames.
func topNames(freq map[string]int) []NameFreq {
	out := make([]NameFreq, 0, len(freq))
	for name, works := range freq {
		out = append(out, NameFreq{Name: name, Works: works})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Works != out[j].Works {
			return out[i].Works > out[j].Works
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > maxTopNames {
		out = out[:maxTopNames]
	}
	return out
}

// collectTag / collectFav append capped samples.
func collectTag(dst *[]TagSample, s TagSample) {
	if len(*dst) < maxSamples {
		*dst = append(*dst, s)
	}
}

func collectFav(dst *[]FavSample, s FavSample) {
	if len(*dst) < maxSamples {
		*dst = append(*dst, s)
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
