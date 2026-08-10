// public_tag_counts.go — the read and the write halves of the one taxonomy
// rollup, kept in the same file because the only thing that makes a rollup
// trustworthy is that both halves mean the same thing.
//
// The write half does NOT contain a second copy of the aggregate. It calls
// workCountsLive — the same function the un-rolled edges are served by, with a
// nil id list standing for "every key" — so the stored numbers are, by
// construction, the numbers the live path would have returned. A rollup filled
// by a re-implementation of the population gate would drift the first time
// either side changed, and the drift would be invisible: both sides would keep
// answering confidently.
//
// See model.CatalogTagWorkCount for why the tag edge earns this and no other
// edge does.
package service

import (
	"context"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// workCountsFromRollup serves a rolled-up edge's counts by primary key. A key
// with no row reads as zero, which is also what the live path returns for a key
// with no reachable work — so a tag minted since the last refresh renders the
// same "0" it would have rendered a moment before it was mapped, rather than an
// error or an omission.
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
		// Nothing matched. Either these tags genuinely reach no work, or the
		// rollup has never been filled — and those two must not render alike,
		// because the second one is a deploy that shipped the read half before
		// anything ran the write half, and it would zero every tag chip on the
		// site. One cheap existence probe tells them apart, and it only runs on
		// the miss path: once the table has a single row, a page of all-zero
		// tags is believed.
		empty, err := s.rollupIsEmpty(ctx, edge)
		if err != nil {
			return nil, nil, err
		}
		if empty {
			return s.workCountsLive(ctx, edge, ids, nsfw)
		}
	}
	for _, r := range rows {
		// Absent-means-zero on both maps, matching workCountsLive, so a consumer
		// cannot tell a zero from a miss — and must not, since they mean the same
		// thing to a reader.
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

// rollupIsEmpty reports whether the rollup table holds no rows at all — the
// "never been refreshed" signal, not a count.
func (s *PublicService) rollupIsEmpty(ctx context.Context, edge taxonomyEdge) (bool, error) {
	var one int
	err := s.db.WithContext(ctx).Raw(`SELECT 1 FROM ` + edge.rollup + ` LIMIT 1`).Scan(&one).Error
	return one == 0, err
}

// TagWorkCountRefresh is one refresh pass's outcome.
type TagWorkCountRefresh struct {
	// Rows is how many tags the rollup now describes.
	Rows int
	// Pruned is how many rows named a tag that no longer reaches any work (a
	// merged-away tag, an unmapped source name, a work that left the gate).
	// They are deleted rather than zeroed: absent and zero already read the
	// same, and keeping them would grow the table by every tag that ever had a
	// work.
	Pruned int
	// ComputedAt is the stamp written on every row this pass, so "how stale is
	// this" is a single MIN over one column.
	ComputedAt time.Time
}

// RefreshTagWorkCounts recomputes every canonical tag's chip counts.
//
// Two passes over the edge, not three: the nsfw caller's tally and the sfw
// caller's tally differ only by the content_rating gate, and the display-axis
// tally is independent of the caller entirely — so pass one (nsfw=true) yields
// n_all AND n_nsfw, and pass two (nsfw=false) yields n_sfw. Running the live
// path twice rather than hand-writing a three-FILTER aggregate is the whole
// point: no new SQL means no new definition of what these counts are.
//
// The swap is one transaction. A reader mid-refresh sees either every old row
// or every new one, never a page whose chips were counted against two different
// populations.
//
// now is passed in rather than read here so a caller can stamp a whole batch of
// maintenance with one time, and so the test can assert on a value it chose.
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

	// The union of the three, because a tag can reach works on one axis and not
	// another: an all-r18 tag has n_all > 0 and n_sfw == 0, and dropping it
	// would make the nsfw caller's chip read 0 instead of its real count.
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
			// Upsert rather than truncate-and-insert: TRUNCATE takes an ACCESS
			// EXCLUSIVE lock that every concurrent work-detail read would queue
			// behind, which is the opposite of what this table is for.
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
