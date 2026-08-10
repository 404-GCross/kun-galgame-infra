package service

import (
	"context"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *PublicService) workCountsFromRollup(ctx context.Context, edge taxonomyEdge, ids []int64, nsfw bool) (counts, nsfwWorks map[int64]int, err error) {
	counts, nsfwWorks = make(map[int64]int, len(ids)), make(map[int64]int, len(ids))
	if len(ids) == 0 {
		return counts, nsfwWorks, nil
	}
	var rows []struct {
		TagID int64 `gorm:"column:tag_id"`
		NAll  int   `gorm:"column:n_all"`
		NSfw  int   `gorm:"column:n_sfw"`
		NNsfw int   `gorm:"column:n_nsfw"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT tag_id, n_all, n_sfw, n_nsfw FROM `+edge.rollup+` WHERE tag_id IN ?`, ids,
	).Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		empty, err := s.rollupIsEmpty(ctx, edge)
		if err != nil {
			return nil, nil, err
		}
		if empty {
			return s.workCountsLive(ctx, edge, ids, nsfw)
		}
	}
	for _, r := range rows {
		n := r.NSfw
		if nsfw {
			n = r.NAll
		}
		if n > 0 {
			counts[r.TagID] = n
		}
		if r.NNsfw > 0 {
			nsfwWorks[r.TagID] = r.NNsfw
		}
	}
	return counts, nsfwWorks, nil
}

func (s *PublicService) rollupIsEmpty(ctx context.Context, edge taxonomyEdge) (bool, error) {
	var one int
	err := s.db.WithContext(ctx).Raw(`SELECT 1 FROM ` + edge.rollup + ` LIMIT 1`).Scan(&one).Error
	return one == 0, err
}

type TagWorkCountRefresh struct {
	Rows       int
	Pruned     int
	ComputedAt time.Time
}

func (s *PublicService) RefreshTagWorkCounts(ctx context.Context, now time.Time) (TagWorkCountRefresh, error) {
	var out TagWorkCountRefresh
	nAll, nNsfw, err := s.workCountsLive(ctx, tagWorkEdge, nil, true)
	if err != nil {
		return out, err
	}
	nSfw, _, err := s.workCountsLive(ctx, tagWorkEdge, nil, false)
	if err != nil {
		return out, err
	}

	keys := make(map[int64]struct{}, len(nAll))
	for id := range nAll {
		keys[id] = struct{}{}
	}
	for id := range nSfw {
		keys[id] = struct{}{}
	}
	for id := range nNsfw {
		keys[id] = struct{}{}
	}

	rows := make([]model.CatalogTagWorkCount, 0, len(keys))
	live := make([]int64, 0, len(keys))
	for id := range keys {
		rows = append(rows, model.CatalogTagWorkCount{
			TagID: id, NAll: nAll[id], NSfw: nSfw[id], NNsfw: nNsfw[id], ComputedAt: now,
		})
		live = append(live, id)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "tag_id"}},
				DoUpdates: clause.AssignmentColumns([]string{"n_all", "n_sfw", "n_nsfw", "computed_at"}),
			}).CreateInBatches(rows, 1000).Error; err != nil {
				return err
			}
		}
		del := tx.Where("computed_at < ?", now)
		if len(live) > 0 {
			del = del.Where("tag_id NOT IN ?", live)
		}
		res := del.Delete(&model.CatalogTagWorkCount{})
		if res.Error != nil {
			return res.Error
		}
		out.Pruned = int(res.RowsAffected)
		return nil
	})
	if err != nil {
		return TagWorkCountRefresh{}, err
	}
	out.Rows, out.ComputedAt = len(rows), now
	return out, nil
}
