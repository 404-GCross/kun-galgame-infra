package tagcanon

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"
)

type Opts struct {
	Apply bool
	DSN   string
	Limit int
}

type JunkSample struct {
	Name   string
	Reason string
}

type GroupSample struct {
	Canonical string
	Tier      int16
	Kind      int16
	Sources   int
	Members   int
}

type Absorption struct {
	SourceID int16
	Key      string
	Names    int
	Absorbed int
}

type Stats struct {
	VndbNames    int
	BangumiNames int
	DlsiteNames  int
	BangumiJunk  int
	JunkByReason map[string]int

	Groups      int
	MetaGroups  int
	TriSource   int
	PlannedMaps int

	TagsCreated  int
	TagsConflict int
	MapsCreated  int
	MapsConflict int
	Errors       int

	Absorptions []Absorption
	Dist        []SourceDist
	JunkSamples []JunkSample
	GroupTop    []GroupSample
}

const maxJunkSamples = 30

func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	db, err := database.OpenJob(opts.DSN)
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
