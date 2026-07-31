// Package tagcanon builds the DETERMINISTIC slice of the tag canonical layer
// (refs/proj/74, design refs/proj/70 §7/§8): it extracts the three per-source
// tag vocabularies (all three = catalog_work_tag grouped by source), mechanically prefilters bangumi junk
// (date/disc/number regexes + maker/label collisions — junk is reported but
// never written), folds the survivors on NFKC+casefold+trim, and mints ONE
// catalog_tag + per-source catalog_tag_source_map rows for every norm spanning
// ≥2 distinct sources. Cross-source groups are tier=core (user ruling); a
// hand-pinned meta set is kind=meta, everything else content. Single-source
// names get NO row this wave (70b's domain).
//
// The ORIGINAL layer (catalog_work_tag) is read-only here — not one row is
// touched. Discipline (55/57/58a/58b lineage):
//   - The DSN is ALWAYS explicit — a bare run cannot touch a live DB. All three
//     vocabularies now live in catalog_work_tag, so one catalog database is the
//     whole input (wave 149: the vndb lane no longer joins the wiki tag layer —
//     see loadWorkTagVocab).
//   - Dry-run is the default: the decided plan (vocab sizes, junk digest, group
//     count, per-source absorption, single-source distribution) is identical in
//     dry and apply; only Tags/Maps Created/Conflict need --apply.
//   - Idempotent: ON CONFLICT DO NOTHING on both tables — a second --apply
//     writes zero, counting the no-ops as conflicts.
//   - Limit windows the (Norm-sorted) group list for a small-sample apply.
package tagcanon

import (
	"context"
	"fmt"
	"log/slog"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Opts configures a run.
type Opts struct {
	Apply bool
	DSN   string
	Limit int // 0 = all groups; >0 windows the Norm-sorted group list (small-sample apply)
}

// JunkSample is one filtered bangumi junk name for the dry-run digest.
type JunkSample struct {
	Name   string
	Reason string
}

// GroupSample is one decided cross-source group for the dry-run digest.
type GroupSample struct {
	Canonical string
	Tier      int16
	Kind      int16
	Sources   int
	Members   int
}

// Absorption is one source's convergence: how many of its non-junk names landed
// in a kept cross-source group.
type Absorption struct {
	SourceID int16
	Key      string
	Names    int // distinct non-junk names of the source
	Absorbed int // of those, in a cross-source group
}

// Stats reports a run. The plan counters are identical in dry and apply;
// Tags/Maps Created/Conflict are apply-only.
type Stats struct {
	VndbNames    int
	BangumiNames int // AFTER junk filter (names entering grouping)
	DlsiteNames  int
	BangumiJunk  int // filtered out
	JunkByReason map[string]int

	Groups      int // cross-source groups decided (full, before --limit)
	MetaGroups  int
	TriSource   int // groups spanning all three sources
	PlannedMaps int // total member map rows across all groups

	TagsCreated  int
	TagsConflict int
	MapsCreated  int
	MapsConflict int
	Errors       int

	Absorptions []Absorption
	Dist        []SourceDist // single-source usage distribution (统计交付)
	JunkSamples []JunkSample
	GroupTop    []GroupSample // widest groups first (by source count, then members)
}

const maxJunkSamples = 30

// Run extracts the vocabularies, decides the groups, and (apply) writes them.
func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	db, err := gorm.Open(postgres.Open(opts.DSN), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	src, err := resolveSources(ctx, db)
	if err != nil {
		return nil, err
	}
	srcKeys := map[int16]string{src.vndb: sourceKeyVNDB, src.bangumi: sourceKeyBangumi, src.dlsite: sourceKeyDlsite}

	// ── vocabulary extraction ────────────────────────────────────────────────
	vndb, err := loadWorkTagVocab(ctx, db, src.vndb)
	if err != nil {
		return nil, err
	}
	bgm, err := loadWorkTagVocab(ctx, db, src.bangumi)
	if err != nil {
		return nil, err
	}
	dl, err := loadWorkTagVocab(ctx, db, src.dlsite)
	if err != nil {
		return nil, err
	}
	labelNorms, err := loadLabelNorms(ctx, db)
	if err != nil {
		return nil, err
	}

	st := &Stats{JunkByReason: map[string]int{}}

	// ── bangumi junk prefilter (mark, count, sample — write NOTHING) ──────────
	for i := range bgm {
		if reason := bgmJunk(bgm[i].Norm, labelNorms); reason != "" {
			bgm[i].Junk = true
			bgm[i].JunkReason = reason
			st.BangumiJunk++
			st.JunkByReason[reason]++
			if len(st.JunkSamples) < maxJunkSamples {
				st.JunkSamples = append(st.JunkSamples, JunkSample{Name: bgm[i].Name, Reason: reason})
			}
		}
	}
	st.VndbNames = len(vndb)
	st.DlsiteNames = len(dl)
	st.BangumiNames = len(bgm) - st.BangumiJunk

	vocab := make([]vocabEntry, 0, len(vndb)+len(bgm)+len(dl))
	vocab = append(vocab, vndb...)
	vocab = append(vocab, bgm...)
	vocab = append(vocab, dl...)

	// ── grouping + stamping ───────────────────────────────────────────────────
	groups := buildGroups(vocab)
	st.Groups = len(groups)
	for _, g := range groups {
		st.PlannedMaps += len(g.Members)
		if g.Kind == 1 {
			st.MetaGroups++
		}
		if g.sourceCount >= 3 {
			st.TriSource++
		}
	}
	st.Absorptions = absorptions(vocab, groups, srcKeys)
	st.Dist = singleSourceDist(vocab, groups, srcKeys)
	st.GroupTop = topGroups(groups)

	// ── write (apply) ─────────────────────────────────────────────────────────
	writeGroups := groups
	if opts.Limit > 0 && opts.Limit < len(writeGroups) {
		writeGroups = writeGroups[:opts.Limit]
	}
	w := &writer{db: db, stats: st}
	for _, g := range writeGroups {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		w.writeGroup(ctx, g, opts.Apply)
	}

	logSummary(st, opts)
	return st, nil
}

// absorptions computes per-source convergence: of a source's non-junk names,
// how many folded into a kept cross-source group.
func absorptions(vocab []vocabEntry, groups []group, srcKeys map[int16]string) []Absorption {
	inGroup := map[string]struct{}{}
	for _, g := range groups {
		inGroup[g.Norm] = struct{}{}
	}
	names := map[int16]int{}
	absorbed := map[int16]int{}
	for _, e := range vocab {
		if e.Junk {
			continue
		}
		names[e.SourceID]++
		if _, ok := inGroup[e.Norm]; ok {
			absorbed[e.SourceID]++
		}
	}
	out := make([]Absorption, 0, len(names))
	for id := range srcKeys {
		out = append(out, Absorption{SourceID: id, Key: srcKeys[id], Names: names[id], Absorbed: absorbed[id]})
	}
	sortByID(out)
	return out
}

func logSummary(st *Stats, opts Opts) {
	slog.Info("tagcanon done", "apply", opts.Apply,
		"vndb_names", st.VndbNames, "bangumi_names", st.BangumiNames, "dlsite_names", st.DlsiteNames,
		"bangumi_junk", st.BangumiJunk, "groups", st.Groups, "meta_groups", st.MetaGroups,
		"tri_source", st.TriSource, "planned_maps", st.PlannedMaps,
		"tags_created", st.TagsCreated, "tags_conflict", st.TagsConflict,
		"maps_created", st.MapsCreated, "maps_conflict", st.MapsConflict, "errors", st.Errors)
	for reason, n := range st.JunkByReason {
		slog.Info("tagcanon junk", "reason", reason, "count", n)
	}
	for _, a := range st.Absorptions {
		slog.Info("tagcanon absorption", "source", a.Key, "names", a.Names, "absorbed", a.Absorbed)
	}
	for _, d := range st.Dist {
		slog.Info("tagcanon single-source dist", "source", d.Key,
			"ge100", d.Buckets.GE100, "ge50", d.Buckets.GE50, "ge20", d.Buckets.GE20,
			"ge10", d.Buckets.GE10, "total", d.Buckets.Total)
	}
	for _, j := range st.JunkSamples {
		slog.Info("tagcanon junk sample", "name", j.Name, "reason", j.Reason)
	}
	for _, g := range st.GroupTop {
		kind := "content"
		if g.Kind == 1 {
			kind = "meta"
		}
		slog.Info("tagcanon group", "canonical", g.Canonical, "tier", g.Tier, "kind", kind,
			"sources", g.Sources, "members", g.Members)
	}
}
