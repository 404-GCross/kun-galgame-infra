// Package releasemeta backfills two release-metadata scalars in one field PR
// (refs/proj/66, governed by the refs/proj/61 field ledger):
//
//   - A · release dates: empty catalog_release.released_y/m/d trios filled
//     from each release's OWN anchor source (ownership, not cross-source
//     arbitration — the anchor decides):
//     · dlsite lane: releases with a DLsite EXACT release anchor read
//     dlsite.works.regist_date (--dlsite-dsn). The timestamptz is rendered
//     at Asia/Shanghai — the wall clock the mirror ingest and the step-55/56
//     importers (splitDate on a +08 box) used, so re-derived dates match the
//     already-imported ones exactly (surveyed: no hour-23 rows, so this also
//     equals the JST date on every row).
//     · eg lane: BODYLESS works with an EG EXACT WORK anchor whose stub
//     (exactly one live) release is still empty read erogamespace
//     games.sellday (--eg-dsn, text 'YYYY-MM-DD'). EG anchors are
//     work-level, so the 1:1 stub restriction keeps the work→release date
//     projection unambiguous.
//     · bgm lane: works with an EXACT Bangumi WORK anchor (any matched_by —
//     the 56a title+year rule, the wiki bid registry and the xmedia import
//     all assert identity) whose stub release is still empty read
//     src_bangumi.subject.date (a schema inside the catalog DB, single
//     --dsn; free text, parsed tolerantly — a bare year is a legal partial
//     date, the columns are a nullable trio).
//     Precedence dlsite > eg > bgm at VALUE level (SKU precision beats
//     work-level projection): a release the dlsite lane could fill is never
//     touched by eg/bgm; when the dlsite mirror has no regist_date (the
//     surveyed live case: ALL currently-empty anchored worknos are mirror
//     NULLs — the importers already consumed every published date) the later
//     lanes may fill.
//
//   - B · age rating: catalog_work.content_rating=0 rows (0 = unrated by
//     step-11 ruling; the vocabulary is doc-14's three tiers — 0 all_ages /
//     1 sensitive / 2 r18) filled by source PRIORITY, high to low:
//     ① DLsite age_category via the work's release anchors ('3'→r18,
//     '2'→sensitive, '1'→all_ages — the importer/dlsite.go dlContentRating
//     mapping), ② a claimed work's wiki galgame.age_limit (--wiki-dsn;
//     surveyed values: 'r18'→r18, 'all'→all_ages — the mapping step-10
//     deferred), ③ Bangumi subject.nsfw=true→r18 as fallback ONLY
//     (nsfw=false is NOT an all-ages statement and never produces a verdict,
//     doc 17 §6). The FIRST source with a verdict wins — ownership, not
//     strictest-across-sources arbitration (v1 ruling, spec-pinned): an
//     explicit higher-priority all_ages verdict keeps the row 0 and
//     suppresses lower lanes.
//
// Discipline (step-10 precedent; 55/62 CLI lineage):
//   - FILL-EMPTY-ONLY: candidates are selected empty and the UPDATE re-asserts
//     emptiness (released_y IS NULL/0, content_rating=0) at the last moment —
//     a non-zero value is NEVER overwritten. This is naturally idempotent: a
//     second pass finds zero candidates to write.
//   - NO upsert / refresh loop, in deliberate contrast to step-62's
//     change-detected upserts: dates and ratings are non-volatile scalars —
//     a mirror refresh reruns the same fill-empty pass and only tops up rows
//     that are still empty.
//   - The survivorship engine (internal/platform/catalog/service/
//     survivorship.go) stays UNUSED — spec-pinned; fill-empty is the whole
//     policy here.
//   - Both fields are catalog IDENTITY-layer scalars (catalog_release is
//     registry-grade; content_rating lives on catalog_work), so CLAIMED works
//     are legal targets (no §8.D bodyless XOR guard — that guard protects
//     media/read-face facet rows, which this tool never writes) and no medium
//     pin applies (the ASMR zero-facet ruling is about facet investment, not
//     completing registry scalars on rows that already exist).
//   - Every DSN is ALWAYS explicit; dry-run is the default and decides the
//     identical plan; Limit/Offset window each lane's candidate list.
package releasemeta

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// maxSamples caps how many per-lane example rows a run collects.
const maxSamples = 8

// minYear is the sanity floor for a fillable release year (the oldest EG entry
// is 1979; nothing catalogued predates the home-computer era).
const minYear = 1950

// Opts configures a run. Apply=false is a dry-run forecast (no writes). All
// four DSNs are REQUIRED and never defaulted: DSN (catalog, which also hosts
// src_bangumi), DlsiteDSN / EGDSN (the staging mirrors) and WikiDSN (the
// galgame-family DB — kun_galgame_wiki locally, kun_catalog in prod since W5).
// Limit/Offset window each lane's candidate list (0 = all).
type Opts struct {
	Apply     bool
	DSN       string
	DlsiteDSN string
	EGDSN     string
	WikiDSN   string
	Limit     int
	Offset    int
}

// DateSample is one example planned date fill for dry-run logging / tests.
type DateSample struct {
	WorkID    int64
	ReleaseID int64
	Ext       string // workno / EG game id / bangumi subject id
	Y         int16
	M, D      *int16
}

// RatingSample is one example planned rating fill (or explicit all-ages
// verdict) for dry-run logging / tests.
type RatingSample struct {
	WorkID int64
	Source string // "dlsite" / "wiki" / "bangumi"
	Ext    string
	Rating int16
}

// Stats reports a run's outcome. The *Planned counters are the decided plan
// (identical in dry and apply); *Filled and *SkippedNonEmpty (the UPDATE's
// last-moment fill-empty guard found the row already non-empty) are apply-only.
type Stats struct {
	// date/dlsite lane
	DlDateCandidates      int // empty-date releases carrying a DLsite exact anchor
	DlDateMissingMirror   int // anchored workno absent from the mirror
	DlDateNoRegist        int // mirror row with NULL regist_date → nothing to fill
	DlDateOutOfRange      int // regist year outside the sanity gate (placeholder)
	DlDatePlanned         int
	DlDateFilled          int
	DlDateSkippedNonEmpty int

	// date/eg lane
	EgDateCandidates      int // bodyless 1:1-stub works w/ EG exact work anchor, date empty
	EgDateCovered         int // stub release already planned by the dlsite lane
	EgDateMultiAnchor     int // extra anchors collapsed (lowest mirror-present id wins)
	EgDateMissingMirror   int
	EgDateBadDate         int // sellday unparseable / outside the sanity gate
	EgDatePlanned         int
	EgDateFilled          int
	EgDateSkippedNonEmpty int

	// date/bgm lane
	BgmDateCandidates      int // 1:1-stub works w/ exact bgm work anchor, date empty
	BgmDateCovered         int // stub release already planned by an earlier lane
	BgmDateNoDate          int // subject.date empty
	BgmDateBadDate         int // unparseable / outside the sanity gate
	BgmDatePartial         int // planned with a year but missing month and/or day
	BgmDatePlanned         int
	BgmDateFilled          int
	BgmDateSkippedNonEmpty int

	// age-rating lane (verdict counters by source × tier)
	RatingCandidates      int // works with content_rating=0
	RatingDlR18           int
	RatingDlSensitive     int
	RatingDlAllAges       int // explicit DLsite all-ages — stays 0, suppresses lower lanes
	RatingWikiR18         int
	RatingWikiAllAges     int // explicit wiki 'all' — stays 0, suppresses bgm
	RatingWikiUnmapped    int // unexpected age_limit value → no verdict from wiki
	RatingBgmR18          int // nsfw=true fallback
	RatingNoVerdict       int // no source had a verdict → stays unrated
	RatingPlanned         int // verdicts > 0 (actual planned writes)
	RatingFilled          int
	RatingSkippedNonEmpty int
	// RatingCuratedOverride counts candidates skipped because a human edited
	// catalog.work.content_rating through the editing engine.
	RatingCuratedOverride int

	Errors int

	DlDateSamples  []DateSample
	EgDateSamples  []DateSample
	BgmDateSamples []DateSample
	RatingSamples  []RatingSample
}

// Run resolves all lanes' candidates and forecasts (dry) or fills (apply) the
// empty scalars. Returns a loggable Stats.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	if opts.DlsiteDSN == "" {
		return nil, fmt.Errorf("DLsite mirror DSN is required (--dlsite-dsn); refusing to guess")
	}
	if opts.EGDSN == "" {
		return nil, fmt.Errorf("EG mirror DSN is required (--eg-dsn); refusing to guess")
	}
	if opts.WikiDSN == "" {
		return nil, fmt.Errorf("galgame-family DSN is required (--wiki-dsn); refusing to guess")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer closeDB(db)
	dlDB, err := openGorm(opts.DlsiteDSN)
	if err != nil {
		return nil, fmt.Errorf("connect DLsite mirror db: %w", err)
	}
	defer closeDB(dlDB)
	egDB, err := openGorm(opts.EGDSN)
	if err != nil {
		return nil, fmt.Errorf("connect EG mirror db: %w", err)
	}
	defer closeDB(egDB)
	wikiDB, err := openGorm(opts.WikiDSN)
	if err != nil {
		return nil, fmt.Errorf("connect galgame-family db: %w", err)
	}
	defer closeDB(wikiDB)

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	st := &Stats{}
	w := &writer{db: db, stats: st}
	maxYear := time.Now().Year() + 3 // sanity ceiling; rejects 2050/2099 placeholders
	// planned marks release ids an earlier lane already decided to fill this
	// run — the value-level dlsite > eg > bgm precedence.
	planned := map[int64]bool{}

	if err := runDlsiteDateLane(ctx, db, dlDB, w, reg, opts, maxYear, planned); err != nil {
		return nil, err
	}
	if err := runEgDateLane(ctx, db, egDB, w, reg, opts, maxYear, planned); err != nil {
		return nil, err
	}
	if err := runBgmDateLane(ctx, db, w, reg, opts, maxYear, planned); err != nil {
		return nil, err
	}
	if err := runRatingLane(ctx, db, dlDB, wikiDB, w, reg, opts); err != nil {
		return nil, err
	}
	if err := w.touch(ctx); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	slog.Info("backfill-release-meta done", "apply", opts.Apply,
		"dl_date_candidates", st.DlDateCandidates, "dl_date_missing_mirror", st.DlDateMissingMirror,
		"dl_date_no_regist", st.DlDateNoRegist, "dl_date_out_of_range", st.DlDateOutOfRange,
		"dl_date_planned", st.DlDatePlanned, "dl_date_filled", st.DlDateFilled,
		"dl_date_skipped_non_empty", st.DlDateSkippedNonEmpty,
		"eg_date_candidates", st.EgDateCandidates, "eg_date_covered", st.EgDateCovered,
		"eg_date_multi_anchor", st.EgDateMultiAnchor, "eg_date_missing_mirror", st.EgDateMissingMirror,
		"eg_date_bad_date", st.EgDateBadDate, "eg_date_planned", st.EgDatePlanned,
		"eg_date_filled", st.EgDateFilled, "eg_date_skipped_non_empty", st.EgDateSkippedNonEmpty,
		"bgm_date_candidates", st.BgmDateCandidates, "bgm_date_covered", st.BgmDateCovered,
		"bgm_date_no_date", st.BgmDateNoDate, "bgm_date_bad_date", st.BgmDateBadDate,
		"bgm_date_partial", st.BgmDatePartial, "bgm_date_planned", st.BgmDatePlanned,
		"bgm_date_filled", st.BgmDateFilled, "bgm_date_skipped_non_empty", st.BgmDateSkippedNonEmpty,
		"rating_candidates", st.RatingCandidates,
		"rating_dl_r18", st.RatingDlR18, "rating_dl_sensitive", st.RatingDlSensitive,
		"rating_dl_all_ages", st.RatingDlAllAges,
		"rating_wiki_r18", st.RatingWikiR18, "rating_wiki_all_ages", st.RatingWikiAllAges,
		"rating_wiki_unmapped", st.RatingWikiUnmapped, "rating_bgm_r18", st.RatingBgmR18,
		"rating_no_verdict", st.RatingNoVerdict, "rating_planned", st.RatingPlanned,
		"rating_filled", st.RatingFilled, "rating_skipped_non_empty", st.RatingSkippedNonEmpty,
		"rating_curated_override", st.RatingCuratedOverride,
		"errors", st.Errors)
	logDateSamples("dlsite", st.DlDateSamples)
	logDateSamples("eg", st.EgDateSamples)
	logDateSamples("bgm", st.BgmDateSamples)
	for _, s := range st.RatingSamples {
		slog.Info("backfill-release-meta rating sample",
			"work_id", s.WorkID, "source", s.Source, "ext", s.Ext, "content_rating", s.Rating)
	}
	return st, nil
}

func logDateSamples(lane string, samples []DateSample) {
	for _, s := range samples {
		slog.Info("backfill-release-meta date sample", "lane", lane,
			"work_id", s.WorkID, "release_id", s.ReleaseID, "ext", s.Ext,
			"y", s.Y, "m", derefOr(s.M, 0), "d", derefOr(s.D, 0))
	}
}

// collectDate / collectRating append capped samples.
func collectDate(dst *[]DateSample, s DateSample) {
	if len(*dst) < maxSamples {
		*dst = append(*dst, s)
	}
}

func collectRating(dst *[]RatingSample, s RatingSample) {
	if len(*dst) < maxSamples {
		*dst = append(*dst, s)
	}
}

func derefOr(p *int16, fallback int16) int16 {
	if p == nil {
		return fallback
	}
	return *p
}

// window applies the offset/limit chunking to an already-distinct candidate
// list (slicing keeps it obviously correct — the bgmsummaries discipline).
func window[T any](in []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[offset:]
	}
	if limit > 0 && limit < len(in) {
		in = in[:limit]
	}
	return in
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
