package seriesorder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// BackfillSources are the lanes whose membership rows predate wave 184 and
// therefore sit at position/kind 0. The derived lane is deliberately absent:
// its builder assigns the facets as it materializes a series, with its own
// fallback kind, and a backfill pass over it would fight that owner every run.
var BackfillSources = []string{"dlsite", "curated"}

// BackfillOpts configures a backfill run. Apply=false is the repo's dry-run
// default; DSN is REQUIRED and never guessed.
type BackfillOpts struct {
	Apply    bool
	DSN      string
	Receipts string
}

// BackfillStats reports a run. In a dry run MembersChanged is the forecast; in
// an apply it is what was written. A second apply pass over unchanged data
// reports zeros for both it and TouchedWorks.
type BackfillStats struct {
	Series          int
	SeriesWithOrder int // series that had at least one row to move
	Members         int
	MembersChanged  int
	TouchedWorks    int
}

// backfillReceipt is one series' decision.
type backfillReceipt struct {
	SeriesID    int64        `json:"series_id"`
	Source      string       `json:"source"`
	ExternalID  string       `json:"external_id"`
	DisplayName string       `json:"display_name"`
	Members     []Assignment `json:"members"`
	Changed     []int64      `json:"changed_work_ids,omitempty"`
}

// Backfill gives every pre-184 dlsite / curated membership row its position and
// kind. It is a plain reconcile, not a one-shot: re-running it is safe and
// writes nothing once the facets agree with the works' release dates and edges.
func Backfill(ctx context.Context, opts BackfillOpts) (*BackfillStats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess")
	}
	db, err := database.OpenJob(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	return BackfillWithDB(ctx, db, opts)
}

// BackfillWithDB is Backfill against an already-open pool (the tests' entry).
func BackfillWithDB(ctx context.Context, db *gorm.DB, opts BackfillOpts) (*BackfillStats, error) {
	var series []struct {
		ID          int64  `gorm:"column:id"`
		Source      string `gorm:"column:source"`
		ExternalID  string `gorm:"column:external_id"`
		DisplayName string `gorm:"column:display_name"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT s.id, src.key AS source, s.external_id, s.display_name
		FROM catalog_series s
		JOIN catalog_source src ON src.id = s.source_id
		WHERE src.key IN ?
		ORDER BY s.id`, BackfillSources).Scan(&series).Error; err != nil {
		return nil, fmt.Errorf("load series: %w", err)
	}

	// Membership for every series in one query, then one facts load for every
	// work: the whole backfill is three reads plus the UPDATEs it really needs.
	membersBySeries := map[int64][]int64{}
	var allWorks []int64
	{
		var rows []struct {
			SeriesID int64 `gorm:"column:series_id"`
			WorkID   int64 `gorm:"column:work_id"`
		}
		if err := db.WithContext(ctx).Raw(`
			SELECT m.series_id, m.work_id
			FROM catalog_series_member m
			JOIN catalog_series s ON s.id = m.series_id
			JOIN catalog_source src ON src.id = s.source_id
			WHERE src.key IN ?`, BackfillSources).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("load members: %w", err)
		}
		for _, r := range rows {
			membersBySeries[r.SeriesID] = append(membersBySeries[r.SeriesID], r.WorkID)
			allWorks = append(allWorks, r.WorkID)
		}
	}
	facts, err := LoadFacts(ctx, db, allWorks)
	if err != nil {
		return nil, fmt.Errorf("load ordering facts: %w", err)
	}

	var log *os.File
	if opts.Receipts != "" {
		if log, err = os.Create(opts.Receipts); err != nil {
			return nil, fmt.Errorf("receipts: %w", err)
		}
		defer log.Close()
	}
	enc := json.NewEncoder(log)

	st := &BackfillStats{Series: len(series)}
	var touched []int64
	for _, s := range series {
		members := membersBySeries[s.ID]
		if len(members) == 0 {
			continue
		}
		st.Members += len(members)
		// The pre-existing lanes get SeriesMemberKindUnknown as the fallback:
		// a dlsite series groups works its source declared related without
		// saying how, and inventing "main" for them would publish a guess.
		want := facts.Assign(members, model.SeriesMemberKindUnknown)
		have, err := LoadCurrent(ctx, db, s.ID)
		if err != nil {
			return nil, fmt.Errorf("series %d current: %w", s.ID, err)
		}
		changed, err := Apply(ctx, db, s.ID, want, have, opts.Apply)
		if err != nil {
			return nil, fmt.Errorf("series %d apply: %w", s.ID, err)
		}
		if len(changed) > 0 {
			st.SeriesWithOrder++
			st.MembersChanged += len(changed)
			touched = append(touched, changed...)
		}
		if log != nil {
			rec := backfillReceipt{
				SeriesID: s.ID, Source: s.Source, ExternalID: s.ExternalID,
				DisplayName: s.DisplayName, Members: want, Changed: changed,
			}
			if err := enc.Encode(rec); err != nil {
				return nil, fmt.Errorf("receipts: %w", err)
			}
		}
	}

	// A member's position/kind is part of what the series block renders, so a
	// work whose row moved is a work the changes feed must re-emit.
	if opts.Apply && len(touched) > 0 {
		if err := repository.TouchWorks(ctx, db, touched); err != nil {
			return nil, fmt.Errorf("touch works: %w", err)
		}
	}
	if opts.Apply {
		st.TouchedWorks = len(dedupeIDs(touched))
	}
	return st, nil
}

func dedupeIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
