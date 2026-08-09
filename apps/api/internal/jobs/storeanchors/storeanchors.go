// Package storeanchors mints RELEASE-GRAIN store identity anchors from VNDB's
// own curated external-link pool (wave 197): Steam appids, DMM content ids and
// DLsite worknos land as EXACT catalog_external_ref rows on the individual
// release they identify.
//
// WHY RELEASE GRAIN. A store page is a SKU, not a work. One visual novel ships
// as a Steam edition, a DMM download, a DLsite doujin release and a boxed
// package, each with its own store id, its own price and its own age rating.
// Wave 91's `storerefs` hung Steam/DMM ids on the WORK because EG's mirror only
// records them there; this job fixes the grain where a first-party source
// states it per release.
//
// WHY EXACT, AND WHY THAT IS NOT A CONTRADICTION. The chain is
//
//	catalog_release --(exact, source=vndb)--> src_vndb.releases
//	  --> src_vndb.releases_extlinks --> src_vndb.extlinks (site = steam/dmm/…)
//
// VNDB records the store id ON THE RELEASE, and that release is already
// anchored EXACT to VNDB, so the store ref is exactly as strong as the ref it
// rides on — the wave-167 `getchurefs` argument, verbatim. EG's
// `games.steam` / `games.dmm` columns stay PROBABLE for the opposite reason:
// they are community cross-references of unknown provenance carried at the
// wrong grain. A PROBABLE vndb anchor never mints an EXACT store one.
//
// WHAT THIS JOB REFUSES TO DO:
//
//   - Re-point an anchor. A release already holding any ref from a lane's
//     source keeps it, whatever its tier or value.
//   - Arbitrate ambiguity. uq_catalog_external_ref_exact admits one holder per
//     (source, external_id, entity_type). When VNDB lists one store id on
//     several anchored releases — a base Steam release and its uncensored
//     sibling, say — there is no release-level evidence for choosing a winner,
//     so ALL of them are skipped (skipped_ambiguous). Picking by lowest id
//     would be fabricating a decision.
//   - Steal a held id. An id already held EXACT by another release is skipped
//     (skipped_value_taken). At the dlsite lane that counter is large and
//     meaningful: it is the vndb-imported and dlsite-imported release
//     populations describing the same SKU twice, a merge worklist, not noise.
//   - Guess at a URL. DMM values are URL paths; landing and listing pages
//     carry no content id and are rejected (skipped_malformed), never parsed
//     into something id-shaped.
//
// getchu is deliberately absent: wave 167's `getchurefs` already owns that lane.
//
// Discipline: InsertRefIfAbsent (never re-grade); catalog_match_rejection
// preloaded and honoured; dry-run default; every DSN explicit; a second --apply
// writes zero.
package storeanchors

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// maxSamples caps how many per-lane example rows a run collects for logging.
const maxSamples = 8

// Opts configures one run.
type Opts struct {
	Apply bool
	DSN   string // catalog DSN — REQUIRED (also hosts src_vndb)
	Only  string // one lane name; empty = every lane
	Limit int    // cap candidates fetched per lane (0 = all); rehearsal aid
}

// LaneStats reports one lane. Every counter except Written and Conflict is
// identical in dry and apply mode: the plan is decided before --apply is
// consulted. The skipped_* counters are mutually exclusive and, together with
// Planned, account for every candidate.
type LaneStats struct {
	Candidates        int // (anchored release, raw value) pairs the chain yielded
	Planned           int
	Written           int
	Conflict          int // ON CONFLICT DO NOTHING fired (concurrent writer)
	Errors            int
	SkippedMalformed  int // value carries no product id — never guessed
	SkippedRejection  int // blocked by catalog_match_rejection
	SkippedValueTaken int // id already held EXACT by another release
	SkippedAmbiguous  int // id maps to several candidate releases in this run
	SkippedDedup      int // the exact same (release, id) pair appeared twice
	// TakenSamples lists a few ids blocked by an existing exact holder, the
	// entry point into the duplicate-release worklist.
	TakenSamples []string
	// AmbiguousSamples lists a few ids that map to several releases.
	AmbiguousSamples []string
}

// Stats reports a run, keyed by lane name in the order the lanes ran.
type Stats struct {
	Order []string
	Lanes map[string]*LaneStats
}

// Run opens the catalog pool and executes the selected lanes.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess the target database")
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	defer closeGorm(db)
	return RunWithDB(ctx, db, opts)
}

// RunWithDB is the pool-agnostic core; tests inject their own handle.
func RunWithDB(ctx context.Context, db *gorm.DB, opts Opts) (*Stats, error) {
	selected, err := selectedLanes(opts.Only)
	if err != nil {
		return nil, err
	}
	vndbSource, err := resolveSource(ctx, db, "vndb")
	if err != nil {
		return nil, err
	}
	st := &Stats{Lanes: map[string]*LaneStats{}}
	for _, l := range selected {
		ls := &LaneStats{}
		st.Order = append(st.Order, l.name)
		st.Lanes[l.name] = ls
		if err := runLane(ctx, db, opts, l, vndbSource, ls); err != nil {
			return nil, fmt.Errorf("lane %s: %w", l.name, err)
		}
	}
	return st, nil
}

// plannedRef is one decided write.
type plannedRef struct {
	releaseID  int64
	workID     int64
	externalID string
}

func runLane(ctx context.Context, db *gorm.DB, opts Opts, l lane, vndbSource int16, ls *LaneStats) error {
	laneSource, err := resolveSource(ctx, db, l.sourceKey)
	if err != nil {
		return err
	}
	cands, err := loadCandidates(ctx, db, vndbSource, laneSource, l.site, opts.Limit)
	if err != nil {
		return fmt.Errorf("load candidates: %w", err)
	}
	ls.Candidates = len(cands)

	taken, err := loadTakenExact(ctx, db, laneSource)
	if err != nil {
		return err
	}
	rejected, err := loadRejections(ctx, db, laneSource)
	if err != nil {
		return err
	}

	plan := decide(cands, l, taken, rejected, ls)
	ls.Planned = len(plan)
	if opts.Apply {
		writePlan(ctx, db, plan, laneSource, l.matchedBy, ls)
	}
	logLane(l.name, opts.Apply, ls)
	return nil
}

// decide turns raw candidates into the write plan, filling in every skip
// counter. It is deliberately a pure function of its inputs so the whole
// filtering doctrine can be tested without a database.
func decide(cands []candidate, l lane, taken, rejected map[string]struct{}, ls *LaneStats) []plannedRef {
	// First pass: normalize, and count how many distinct releases each id
	// wants. Ambiguity has to be known before any row is planned, so a lane
	// cannot plan the first occurrence and skip the rest.
	type normalized struct {
		candidate
		ext string
	}
	norm := make([]normalized, 0, len(cands))
	holders := map[string]map[int64]struct{}{}
	for _, c := range cands {
		ext := l.normalize(c.RawValue)
		if ext == "" {
			ls.SkippedMalformed++
			continue
		}
		norm = append(norm, normalized{candidate: c, ext: ext})
		if holders[ext] == nil {
			holders[ext] = map[int64]struct{}{}
		}
		holders[ext][c.ReleaseID] = struct{}{}
	}

	seen := map[string]struct{}{}
	plan := make([]plannedRef, 0, len(norm))
	for _, n := range norm {
		key := rejKey(n.ReleaseID, n.ext)
		if _, dup := seen[key]; dup {
			ls.SkippedDedup++
			continue
		}
		seen[key] = struct{}{}
		if _, hit := rejected[key]; hit {
			ls.SkippedRejection++
			continue
		}
		if _, hit := taken[n.ext]; hit {
			ls.SkippedValueTaken++
			addSample(&ls.TakenSamples, n.ext)
			continue
		}
		if len(holders[n.ext]) > 1 {
			ls.SkippedAmbiguous++
			addSample(&ls.AmbiguousSamples, n.ext)
			continue
		}
		plan = append(plan, plannedRef{releaseID: n.ReleaseID, workID: n.WorkID, externalID: n.ext})
	}
	return plan
}

// writePlan inserts the planned rows and touches the parent works that really
// gained an anchor, so the public changes feed sees the new store link.
func writePlan(ctx context.Context, db *gorm.DB, plan []plannedRef, laneSource int16, matchedBy string, ls *LaneStats) {
	var touched []int64
	for _, p := range plan {
		wrote, err := repository.InsertRefIfAbsent(db.WithContext(ctx), model.CatalogExternalRef{
			EntityType: model.EntityTypeRelease, EntityID: p.releaseID,
			SourceID: laneSource, ExternalID: p.externalID,
			LinkKind: model.LinkKindExact, MatchedBy: matchedBy,
		})
		switch {
		case err != nil:
			ls.Errors++
			slog.Warn("write store anchor", "release", p.releaseID,
				"source", laneSource, "ext", p.externalID, "err", err)
		case wrote:
			ls.Written++
			touched = append(touched, p.workID)
		default:
			ls.Conflict++
		}
	}
	if err := repository.TouchWorks(ctx, db, touched); err != nil {
		ls.Errors++
		slog.Warn("touch works", "err", err)
	}
}

func addSample(dst *[]string, v string) {
	if len(*dst) < maxSamples {
		*dst = append(*dst, v)
	}
}

func logLane(name string, apply bool, ls *LaneStats) {
	sort.Strings(ls.TakenSamples)
	sort.Strings(ls.AmbiguousSamples)
	slog.Info("store-anchors lane done", "lane", name, "apply", apply,
		"candidates", ls.Candidates, "planned", ls.Planned,
		"written", ls.Written, "conflict", ls.Conflict, "errors", ls.Errors,
		"skipped_malformed", ls.SkippedMalformed,
		"skipped_rejection", ls.SkippedRejection,
		"skipped_value_taken", ls.SkippedValueTaken,
		"skipped_ambiguous", ls.SkippedAmbiguous,
		"skipped_dedup", ls.SkippedDedup)
}

func unknownLaneError(only string) error {
	names := make([]string, 0, len(lanes))
	for _, l := range lanes {
		names = append(names, l.name)
	}
	return fmt.Errorf("unknown lane %q (want one of %v, or empty for all)", only, names)
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeGorm(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}
}
