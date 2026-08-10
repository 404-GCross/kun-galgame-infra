package bgmzhnames

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/infrastructure/database"

	"gorm.io/gorm"
)

const LangZhHans = "zh-Hans"

const sourceBangumi int16 = 3

func bangumiSourceRef() *int16 { s := sourceBangumi; return &s }

const maxSamples = 10

type Opts struct {
	Apply  bool
	DSN    string
	Lane   Lane
	Limit  int
	Offset int
}

type Sample struct {
	EntityID   int64
	OwnerID    int64
	ExternalID string
	Name       string
	Primary    bool
}

type Stats struct {
	Anchored           int
	SkippedNoOwner     int
	SkippedGuard       int
	NoSupply           int
	SkippedNonChinese  int
	SkippedSameAsOwner int
	Candidates         int
	Names              int
	WouldInsert        int
	SkippedDup         int
	Inserted           int
	Conflict           int
	PrimarySet         int
	Touched            int
	Errors             int
	Samples            []Sample
}

func Run(ctx context.Context, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the acceptance run")
	}
	spec, err := laneFor(opts.Lane)
	if err != nil {
		return nil, err
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	sourceID, err := resolveBangumiSource(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := spec.load(ctx, db, sourceID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, err
	}
	st := &Stats{Anchored: len(rows)}

	plans := make([]plan, 0, len(rows))
	for _, r := range rows {
		if r.OwnerID == nil {
			st.SkippedNoOwner++
			continue
		}
		fields, ok := parseFields(r.Infobox)
		if !ok {
			st.SkippedGuard++
			continue
		}
		names, rejected := projectNames(fields)
		st.SkippedNonChinese += rejected
		if spec.dropOwnerName {
			names = dropName(names, r.OwnerName, &st.SkippedSameAsOwner)
		}
		if len(names) == 0 {
			st.NoSupply++
			continue
		}
		st.Candidates++
		st.Names += len(names)
		plans = append(plans, plan{
			EntityID: r.EntityID, OwnerID: *r.OwnerID, ExternalID: r.ExternalID, Names: names,
		})
	}
	owners := make([]int64, len(plans))
	for i, p := range plans {
		owners[i] = p.OwnerID
	}
	existing, err := spec.preload(ctx, db, owners)
	if err != nil {
		return nil, err
	}
	hosts := map[int64][]int64{}
	if spec.hostWorks != nil {
		if hosts, err = spec.hostWorks(ctx, db, owners); err != nil {
			return nil, err
		}
	}
	lane := opts.Lane
	if lane == "" {
		lane = LaneCharacter
	}
	slog.Info("bgm-zh-names candidates", "lane", lane, "anchored", st.Anchored, "candidates", st.Candidates,
		"names", st.Names, "skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)

	w := &writer{db: db, spec: spec, existing: existing, hostWorks: hosts, stats: st, apply: opts.Apply}
	for _, p := range plans {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		w.write(ctx, p)
	}
	if opts.Apply && spec.hostWorks != nil {
		if err := w.flushTouch(ctx); err != nil {
			return nil, fmt.Errorf("touch host works: %w", err)
		}
	}
	slog.Info("bgm-zh-names done", "lane", lane, "apply", opts.Apply,
		"anchored", st.Anchored, "skipped_no_owner", st.SkippedNoOwner,
		"skipped_guard", st.SkippedGuard, "no_supply", st.NoSupply,
		"skipped_non_chinese", st.SkippedNonChinese, "skipped_same_as_owner", st.SkippedSameAsOwner,
		"candidates", st.Candidates, "names", st.Names,
		"would_insert", st.WouldInsert, "skipped_dup", st.SkippedDup,
		"inserted", st.Inserted, "primary_set", st.PrimarySet, "conflict", st.Conflict,
		"touched_works", st.Touched, "errors", st.Errors)
	return st, nil
}

type plan struct {
	EntityID   int64
	OwnerID    int64
	ExternalID string
	Names      []string
}

func dropName(names []string, owner string, counter *int) []string {
	if owner == "" {
		return names
	}
	out := names[:0]
	for _, n := range names {
		if n == owner {
			*counter++
			continue
		}
		out = append(out, n)
	}
	return out
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}
