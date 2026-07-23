// Package worktags backfills catalog_work_tag rows for galgame works from the
// Bangumi folksonomy (refs/proj/58b, doc 51 §9.3 multi-media template; T2 =
// refs/proj/70 §3/§8, 88): EVERY EXACT Bangumi work anchor (matched_by
// unrestricted — the 66/69/71 ruling) joins each work to a src_bangumi.subject
// (a schema INSIDE the catalog DB — single --dsn); the subject's `tags` jsonb
// array ([{name, count}]) lands one row per tag, VERBATIM (58 拍板): no
// vocabulary mapping, no threshold — count=1 rows are stored too (store-all;
// consumers filter when rendering).
//
// T2 admits CLAIMED works too. The (facet, source) XOR (refs/proj/70 §3/§8)
// makes catalog_work_tag the catalog-native SOURCE lane (bangumi) for every
// galgame work — the read face merges it with the wiki bridge lane, so a
// claimed work surfaces both. No claim filter, no write-time guard.
// Content tags NEVER touch catalog_label (the attribution-vocabulary red
// line).
//
// Defensive parsing (all counted, never fatal):
//   - tags NULL / empty array → NoTags (nothing to write, a normal case)
//   - tags not a JSON array of {name,count} objects → NotArray (counted skip)
//   - element with a missing or blank-after-trim name → NameBlank (skipped;
//     names are stored whitespace-trimmed, otherwise verbatim)
//   - duplicate name within one subject → DupCollapsed (highest count kept)
//
// Discipline (55/57/58a lineage, all spec-pinned):
//   - The DSN is ALWAYS explicit — a bare run cannot touch a live DB.
//   - Dry-run is the default: the decided plan (counters + top-frequency tag
//     names + samples) is identical in dry and apply; only Written/Conflict
//     need --apply.
//   - No XOR guard (T2): bgm is a catalog-native source lane; claimed and
//     bodyless works materialize alike (bridge-not-copy still keeps the wiki
//     sources out of this table).
//   - Idempotent: ON CONFLICT (work_id, name, source_id) DO NOTHING (the
//     uq_catalog_work_tag key) — a second --apply writes zero, counting the
//     no-ops as conflicts.
//   - Limit/Offset window the candidate work list (chunking).
package worktags

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ruleTitleYear is the matched_by tag of the step-56a EXACT Bangumi anchors.
// The candidate query no longer filters on it (every exact tier is admitted);
// it remains a representative exact-anchor rule the tests seed with.
const ruleTitleYear = "rule:bgm-title-year"

// maxSamples caps how many example rows a run collects for logging / test
// assertions; maxTopNames caps the top-frequency tag-name digest.
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

// Sample is one example planned row for dry-run logging / test assertions.
type Sample struct {
	WorkID    int64
	SubjectID int64
	Name      string
	Count     int
}

// NameFreq is one entry of the top-frequency tag-name digest: how many works
// the name is planned on.
type NameFreq struct {
	Name  string
	Works int
}

// Stats reports a run's outcome. The plan counters are identical in dry and
// apply; Written/Conflict are apply-only outcomes.
type Stats struct {
	Candidates   int // exact-anchored galgame works (claimed + bodyless) joined to their subject
	NoTags       int // subject tags NULL / empty array → nothing to write
	NotArray     int // subject tags malformed (not an array of {name,count}) → counted skip
	NameBlank    int // element with missing / blank-after-trim name → skipped
	DupCollapsed int // duplicate name within one subject → collapsed to highest count
	Planned      int // decided tag rows
	Written      int // rows inserted (apply)
	Conflict     int // ON CONFLICT no-ops (row already existed)
	Errors       int

	DistinctNames int        // distinct tag names across the plan
	TopNames      []NameFreq // most-frequent tag names across the plan (capped)
	Samples       []Sample
}

// Run resolves the candidates and forecasts (dry) or writes (apply) the tag
// rows. Returns a loggable Stats.
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
		tags := parseSubjectTags(c.Tags, st)
		if tags == nil {
			continue // NoTags / NotArray already counted
		}
		for _, tg := range tags {
			st.Planned++
			nameFreq[tg.Name]++
			collect(&st.Samples, Sample{WorkID: c.WorkID, SubjectID: c.SubjectID, Name: tg.Name, Count: tg.Count})
			w.write(ctx, plannedRow{
				WorkID: c.WorkID, SourceID: reg.bangumiSource,
				Name: tg.Name, Count: tg.Count,
			}, opts.Apply)
		}
	}

	st.DistinctNames = len(nameFreq)
	st.TopNames = topNames(nameFreq)

	slog.Info("backfill-work-tags done", "apply", opts.Apply,
		"candidates", st.Candidates, "no_tags", st.NoTags, "not_array", st.NotArray,
		"name_blank", st.NameBlank, "dup_collapsed", st.DupCollapsed,
		"planned", st.Planned, "distinct_names", st.DistinctNames,
		"written", st.Written, "conflict", st.Conflict,
		"errors", st.Errors)
	for _, nf := range st.TopNames {
		slog.Info("backfill-work-tags top tag", "name", nf.Name, "works", nf.Works)
	}
	for _, s := range st.Samples {
		slog.Info("backfill-work-tags sample", "work_id", s.WorkID, "subject_id", s.SubjectID,
			"name", s.Name, "count", s.Count)
	}
	return st, nil
}

// topNames digests the plan's name→work frequency map into the most-frequent
// tag names (ties broken by name for determinism), capped at maxTopNames.
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

// collect appends a capped Sample.
func collect(dst *[]Sample, s Sample) {
	if len(*dst) >= maxSamples {
		return
	}
	*dst = append(*dst, s)
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}
