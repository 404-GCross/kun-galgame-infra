package tagcanon

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
)

type BacklogOpts struct {
	DSN       string
	Threshold int
	Top       int
}

type BacklogEntry struct {
	Source string
	Name   string
	Usage  int
}

type BacklogStats struct {
	Total          int
	AboveThreshold int
	Threshold      int
	BySource       map[string]int
	AboveBySource  map[string]int
	Top            []BacklogEntry
}

// Backlog answers "is there enough new vocabulary to be worth a wave?" without
// spending a single LLM call: it is the same pool propose would judge — mapped,
// junk and rejected names already removed — counted instead of classified.
func Backlog(ctx context.Context, opts BacklogOpts) (*BacklogStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn)")
	}
	if opts.Threshold <= 0 {
		opts.Threshold = 20
	}
	if opts.Top <= 0 {
		opts.Top = 40
	}
	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	pool, err := buildPool(ctx, db, ProposeOpts{DSN: opts.DSN, SkipOriginals: true})
	if err != nil {
		return nil, err
	}

	st := &BacklogStats{
		Total: len(pool), Threshold: opts.Threshold,
		BySource: map[string]int{}, AboveBySource: map[string]int{},
	}
	above := make([]candName, 0, 128)
	for _, c := range pool {
		st.BySource[c.SourceKey]++
		if c.Usage >= opts.Threshold {
			st.AboveThreshold++
			st.AboveBySource[c.SourceKey]++
			above = append(above, c)
		}
	}
	sort.Slice(above, func(i, j int) bool {
		if above[i].Usage != above[j].Usage {
			return above[i].Usage > above[j].Usage
		}
		return above[i].Name < above[j].Name
	})
	if len(above) > opts.Top {
		above = above[:opts.Top]
	}
	for _, c := range above {
		st.Top = append(st.Top, BacklogEntry{Source: c.SourceKey, Name: c.Name, Usage: c.Usage})
	}

	slog.Info("tag backlog", "total", st.Total, "threshold", st.Threshold,
		"above_threshold", st.AboveThreshold, "by_source", st.BySource,
		"above_by_source", st.AboveBySource)
	return st, nil
}
