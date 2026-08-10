package derivedseries

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"api/internal/jobs/seriesorder"
	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type existingSeries struct {
	id   int64
	name string
}

func reconcile(ctx context.Context, db *gorm.DB, derivedSrc int16, want map[string]*candidate,
	facts *seriesorder.Facts, titles map[int64]string, opts Opts, st *Stats) ([]int64, error) {
	var rows []struct {
		ID          int64  `gorm:"column:id"`
		ExternalID  string `gorm:"column:external_id"`
		DisplayName string `gorm:"column:display_name"`
	}
	if err := db.WithContext(ctx).Raw(`
		SELECT id, external_id, display_name FROM catalog_series WHERE source_id = ?`,
		derivedSrc).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load derived series: %w", err)
	}
	existing := make(map[string]existingSeries, len(rows))
	for _, r := range rows {
		existing[r.ExternalID] = existingSeries{id: r.ID, name: r.DisplayName}
	}

	var log *os.File
	if opts.Receipts != "" {
		var err error
		if log, err = os.Create(opts.Receipts); err != nil {
			return nil, fmt.Errorf("receipts: %w", err)
		}
		defer log.Close()
	}
	enc := json.NewEncoder(log)

	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var touched []int64
	for _, key := range keys {
		cand := want[key]
		e, exists := existing[key]
		seriesID := e.id
		switch {
		case !exists:
			st.SeriesCreated++
			if opts.Apply {
				if err := db.WithContext(ctx).Raw(`
					INSERT INTO catalog_series (display_name, source_id, external_id)
					VALUES (?, ?, ?) RETURNING id`, cand.name, derivedSrc, key).
					Scan(&seriesID).Error; err != nil {
					return nil, fmt.Errorf("series %s insert: %w", key, err)
				}
			}
		case e.name != cand.name:
			st.SeriesRenamed++
			if opts.Apply {
				if err := db.WithContext(ctx).Exec(`
					UPDATE catalog_series SET display_name = ?, updated_at = now() WHERE id = ?`,
					cand.name, seriesID).Error; err != nil {
					return nil, fmt.Errorf("series %s rename: %w", key, err)
				}
			}
			touched = append(touched, cand.works...)
		}

		memberTouched, err := reconcileMembers(ctx, db, seriesID, cand, opts.Apply, st)
		if err != nil {
			return nil, err
		}
		touched = append(touched, memberTouched...)

		assignments := facts.Assign(cand.works, model.SeriesMemberKindMain)
		if seriesID != 0 {
			have, err := seriesorder.LoadCurrent(ctx, db, seriesID)
			if err != nil {
				return nil, fmt.Errorf("series %s current order: %w", key, err)
			}
			changed, err := seriesorder.Apply(ctx, db, seriesID, assignments, have, opts.Apply)
			if err != nil {
				return nil, fmt.Errorf("series %s order: %w", key, err)
			}
			st.OrderChanged += len(changed)
			touched = append(touched, changed...)
		}
		if log != nil {
			if err := enc.Encode(buildReceipt(key, cand, seriesID, assignments, titles)); err != nil {
				return nil, fmt.Errorf("receipts: %w", err)
			}
		}
	}

	extKeys := make([]string, 0, len(existing))
	for k := range existing {
		extKeys = append(extKeys, k)
	}
	sort.Strings(extKeys)
	for _, key := range extKeys {
		if _, ok := want[key]; ok {
			continue
		}
		st.SeriesDeleted++
		if !opts.Apply {
			continue
		}
		var orphaned []int64
		if err := db.WithContext(ctx).Raw(`
			DELETE FROM catalog_series_member WHERE series_id = ? RETURNING work_id`,
			existing[key].id).Scan(&orphaned).Error; err != nil {
			return nil, fmt.Errorf("series %s member cascade: %w", key, err)
		}
		touched = append(touched, orphaned...)
		if err := db.WithContext(ctx).Exec(`DELETE FROM catalog_series WHERE id = ?`,
			existing[key].id).Error; err != nil {
			return nil, fmt.Errorf("series %s delete: %w", key, err)
		}
	}
	return touched, nil
}

func reconcileMembers(ctx context.Context, db *gorm.DB, seriesID int64, cand *candidate,
	apply bool, st *Stats) ([]int64, error) {
	if seriesID == 0 {
		st.MembersAdded += len(cand.works)
		return nil, nil
	}
	var current []int64
	if err := db.WithContext(ctx).Raw(`
		SELECT work_id FROM catalog_series_member WHERE series_id = ?`, seriesID).
		Scan(&current).Error; err != nil {
		return nil, fmt.Errorf("series %s members: %w", cand.externalID, err)
	}
	have := make(map[int64]struct{}, len(current))
	for _, w := range current {
		have[w] = struct{}{}
	}
	wanted := make(map[int64]struct{}, len(cand.works))
	for _, w := range cand.works {
		wanted[w] = struct{}{}
	}

	var touched []int64
	for _, w := range cand.works {
		if _, ok := have[w]; ok {
			continue
		}
		st.MembersAdded++
		touched = append(touched, w)
		if !apply {
			continue
		}
		if err := db.WithContext(ctx).Exec(`
			INSERT INTO catalog_series_member (series_id, work_id)
			VALUES (?, ?) ON CONFLICT (series_id, work_id) DO NOTHING`, seriesID, w).Error; err != nil {
			return nil, fmt.Errorf("series %s member %d insert: %w", cand.externalID, w, err)
		}
	}
	for _, w := range current {
		if _, ok := wanted[w]; ok {
			continue
		}
		st.MembersStale++
		touched = append(touched, w)
		if !apply {
			continue
		}
		if err := db.WithContext(ctx).Exec(`
			DELETE FROM catalog_series_member WHERE series_id = ? AND work_id = ?`,
			seriesID, w).Error; err != nil {
			return nil, fmt.Errorf("series %s member %d delete: %w", cand.externalID, w, err)
		}
	}
	return touched, nil
}

func buildReceipt(key string, cand *candidate, seriesID int64,
	assignments []seriesorder.Assignment, titles map[int64]string) receipt {
	rec := receipt{ExternalID: key, DisplayName: cand.name, NamedBy: cand.namedBy, SeriesID: seriesID}
	for _, a := range assignments {
		rec.Members = append(rec.Members, struct {
			WorkID   int64  `json:"work_id"`
			Position int16  `json:"position"`
			Kind     int16  `json:"kind"`
			Title    string `json:"title"`
		}{WorkID: a.WorkID, Position: a.Position, Kind: a.Kind, Title: titles[a.WorkID]})
	}
	return rec
}
