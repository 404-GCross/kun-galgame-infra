// Package dlsitegenres backfills catalog_work_tag rows for BODYLESS galgame
// works from the DLsite OFFICIAL genre taxonomy (refs/proj/68, the tags
// facet's dlsite sibling of 58b's Bangumi folksonomy lane): the DLsite EXACT
// RELEASE anchors (workno anchors are SKU-natured, doc 17 R3 — the
// workratings/dlsitemedia lane shape) join each bodyless GALGAME-medium work
// (the ASMR family also carries DLsite anchors and is out of scope by ruling)
// to the DLsite mirror's works.product_json.genres[] ({id, name} — the
// embedded name is the work's ja name AT SCRAPE TIME, possibly outdated).
//
// Name resolution (spec-pinned priority, wave 67's vocabulary):
//   - genre_taxonomy (genre_id, locale='zh_CN') in the SAME dlsite mirror DB —
//     the authoritative CURRENT official name. Resolving by id auto-corrects
//     works that embed a since-renamed genre (67 finding: DLsite renames in
//     place, e.g. 113 レイプ→合意なし; the id is the stable key).
//   - no taxonomy row (the 48 retired ids 67 surveyed) → fall back to the
//     embedded ja name, trimmed. Blank-after-trim with no taxonomy row →
//     skipped (counted, never fatal).
//
// Discipline (55/57/58 lineage, all spec-pinned):
//   - Every DSN is ALWAYS explicit — a bare run cannot touch a live DB.
//   - Dry-run is the default: the decided plan (counters incl. the
//     zh-hit/ja-fallback resolution buckets + samples) is identical in dry and
//     apply; only Written/Conflict need --apply.
//   - XOR guard (§8.D): native rows only for bodyless works (site NULL/”);
//     the SQL already filters to bodyless, and the writer re-asserts it.
//   - Idempotent FILL semantics: ON CONFLICT (work_id, name, source_id)
//     DO NOTHING — a second --apply writes zero (no-ops count as conflicts).
//     Deliberately NOT step 62's DO UPDATE refresh upsert: ratings/popularity
//     are volatile counters, genre sets are quasi-static — a re-run after a
//     mirror refresh only adds newly-appeared genres, never mutates rows.
//   - count=0 on every row (DLsite genres carry no votes; 58b pinned count
//     semantics as source-relative, and the read face omits 0 via omitempty).
//   - Same-name dedupe within a work (two ids resolving to one zh name).
//   - Limit/Offset window the candidate work list (chunking).
package dlsitegenres

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// taxonomyLocale is the locale the tag names are localized to — zh_CN per the
// step-68 ruling (the catalog's primary display language).
const taxonomyLocale = "zh_CN"

// maxSamples caps how many example rows a run collects for logging / test
// assertions; maxTopNames caps the top-frequency tag-name digest.
const (
	maxSamples  = 8
	maxTopNames = 10
)

// Opts configures a run.
type Opts struct {
	// Apply=false is a dry-run forecast (no writes). DSN (catalog) and
	// DlsiteDSN (the DLsite mirror, which also hosts genre_taxonomy) are BOTH
	// REQUIRED and never defaulted. Limit/Offset window the candidate work
	// list (0 = all).
	Apply     bool
	DSN       string
	DlsiteDSN string
	Limit     int
	Offset    int
}

// Sample is one example planned row for dry-run logging / test assertions.
// FromTaxonomy tells which resolution bucket named it: the zh_CN taxonomy row
// (true) or the embedded-ja retired-id fallback (false).
type Sample struct {
	WorkID       int64
	Workno       string
	GenreID      int
	Name         string
	FromTaxonomy bool
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
	TaxonomyRows  int // zh_CN genre_taxonomy rows loaded (the 67 vocabulary)
	Candidates    int // bodyless galgame works carrying a DLsite exact release anchor
	MissingMirror int // anchored workno absent from the mirror
	NoGenres      int // mirror row with NULL / empty genres → nothing to write
	NotArray      int // genres malformed (not an array of {id,name}) → counted skip
	ZhHit         int // elements named via the zh_CN taxonomy row (by genre id)
	JaFallback    int // retired ids (no taxonomy row) named via the embedded ja name
	NameBlank     int // no taxonomy row AND blank/missing embedded name → skipped
	DupCollapsed  int // same resolved name twice within one work → collapsed
	Planned       int // decided tag rows
	Written       int // rows inserted (apply)
	Conflict      int // ON CONFLICT no-ops (row already existed)
	Errors        int

	DistinctNames   int        // distinct tag names across the plan
	TopNames        []NameFreq // most-frequent tag names across the plan (capped)
	Samples         []Sample   // first planned rows (both buckets)
	FallbackSamples []Sample   // first retired-id ja-fallback rows specifically
}

// Run resolves the candidates and forecasts (dry) or writes (apply) the tag
// rows. Returns a loggable Stats.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
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
	taxonomy, err := loadTaxonomy(ctx, dlsiteDB)
	if err != nil {
		return nil, fmt.Errorf("load genre taxonomy: %w", err)
	}
	if len(taxonomy) == 0 {
		// An unfilled vocabulary must fail loudly, never silently degrade every
		// genre to its embedded ja name (the prod dlsite DB ships WITHOUT wave
		// 67's rows — they must be copied / re-fetched before the first apply).
		return nil, fmt.Errorf("genre_taxonomy has no %s rows — load wave 67's vocabulary into this mirror first", taxonomyLocale)
	}
	cands, err := loadCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	st := &Stats{TaxonomyRows: len(taxonomy), Candidates: len(cands)}
	w := &writer{db: db, stats: st}
	nameFreq := map[string]int{}

	worknos := make([]string, 0, len(cands))
	for _, c := range cands {
		worknos = append(worknos, c.Workno)
	}
	mirror, err := loadMirrorGenres(ctx, dlsiteDB, worknos)
	if err != nil {
		return nil, fmt.Errorf("load DLsite mirror genres: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		raw, ok := mirror[c.Workno]
		if !ok {
			st.MissingMirror++
			continue
		}
		for _, g := range resolveGenres(raw, taxonomy, st) {
			st.Planned++
			nameFreq[g.Name]++
			s := Sample{WorkID: c.WorkID, Workno: c.Workno, GenreID: g.ID, Name: g.Name, FromTaxonomy: g.FromTaxonomy}
			collect(&st.Samples, s)
			if !g.FromTaxonomy {
				collect(&st.FallbackSamples, s)
			}
			w.write(ctx, plannedRow{
				WorkID: c.WorkID, SourceID: reg.dlsiteSource, Name: g.Name,
			}, opts.Apply)
		}
	}

	if err := w.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	st.DistinctNames = len(nameFreq)
	st.TopNames = topNames(nameFreq)

	slog.Info("backfill-dlsite-genres done", "apply", opts.Apply,
		"taxonomy_rows", st.TaxonomyRows, "candidates", st.Candidates,
		"missing_mirror", st.MissingMirror, "no_genres", st.NoGenres, "not_array", st.NotArray,
		"zh_hit", st.ZhHit, "ja_fallback", st.JaFallback, "name_blank", st.NameBlank,
		"dup_collapsed", st.DupCollapsed, "planned", st.Planned,
		"distinct_names", st.DistinctNames, "written", st.Written, "conflict", st.Conflict,
		"errors", st.Errors)
	for _, nf := range st.TopNames {
		slog.Info("backfill-dlsite-genres top genre", "name", nf.Name, "works", nf.Works)
	}
	for _, s := range st.Samples {
		slog.Info("backfill-dlsite-genres sample", "work_id", s.WorkID, "workno", s.Workno,
			"genre_id", s.GenreID, "name", s.Name, "from_taxonomy", s.FromTaxonomy)
	}
	for _, s := range st.FallbackSamples {
		slog.Info("backfill-dlsite-genres retired-id fallback", "work_id", s.WorkID,
			"workno", s.Workno, "genre_id", s.GenreID, "name", s.Name)
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
