package service

import (
	"context"
	"sort"

	"api/internal/platform/catalog/model"
)

// Popularity read face (step 62, the ratings facet's companion; per-source
// XOR since T2b, refs/proj/102) — the sixth media-aggregation facet. Two
// per-source lanes, source_id on every row:
//
//   - dlsite is BRIDGE-EXCLUSIVE: claimed works pivot galgame_dlsite_meta
//     (the step-62 product co-located in kun_catalog); native dlsite rows on
//     a claimed work stay invisible (shadow-never-delete §8.B).
//   - catalog-native sources (today the Bangumi favorite shelves, metrics
//     10-14) read catalog_work_popularity for claimed and bodyless works
//     alike.
//
// A claimed work's face is the union of both lanes re-sorted (source_id,
// metric) ascending; a bodyless work is the degenerate case (no galgame body
// — no bridge, native rows only).
//
// Bridge column mapping (galgame_dlsite_meta, surveyed 2026-07-21):
//
//	dl_count       → {source: dlsite, metric: 0 downloads, value}
//	wishlist_count → {source: dlsite, metric: 1 wishlist,  value}
//	review_count   → {source: dlsite, metric: 2 reviews,   value}
//
// A NULL counter contributes NO row (DLsite genuinely does not publish
// dl_count for commercial works — absent ≠ 0), while a published 0 does (a
// real value the refresh loop keeps current). Values are VERBATIM per
// (source, metric) — never summed across sources (a DLsite sale and a
// Bangumi favorite are different units; consumers render per source_id +
// metric, exactly the ratings-scale rule).

// WorkPopularityRow is one (source, metric) counter on a work's read face —
// the unified shape the claimed bridge (galgame_dlsite_meta) and the bodyless
// native table (catalog_work_popularity) both project into.
type WorkPopularityRow struct {
	SourceID int16
	Metric   int16
	Value    int64
}

// loadWorkPopularity assembles the popularity set for a set of works, honoring
// the media-aggregation contract with the T2b per-source XOR (refs/proj/51
// §2/§3/§8, step 62, refs/proj/102):
//
//   - CLAIMED (site='galgame_wiki'): the dlsite bridge from galgame_dlsite_meta
//     (see the file doc for the mapping; bridge-not-copy §2) UNION the work's
//     native NON-dlsite rows (today the bgm favorite shelves). Native dlsite
//     rows stay invisible — dlsite is bridge-exclusive (shadow-never-delete).
//   - BODYLESS (site=”/NULL): the work's catalog_work_popularity rows, all
//     sources.
//
// Batched (§9.1): one galgame_dlsite_meta query + one catalog_work_popularity
// query per lane — never per-work. Each work's rows are ordered (source_id,
// metric) ascending. Returns a map keyed by work id; a work with no popularity
// is absent (the caller renders []).
func (s *ReadService) loadWorkPopularity(ctx context.Context, subjects []claimSubject) (map[int64][]WorkPopularityRow, error) {
	out := make(map[int64][]WorkPopularityRow, len(subjects))
	galgameIDs, galgameToWork, bodylessIDs := partitionClaimSubjects(subjects)
	if len(bodylessIDs) > 0 {
		if err := s.nativeWorkPopularity(ctx, bodylessIDs, 0, out); err != nil {
			return nil, err
		}
	}
	if len(galgameIDs) == 0 {
		return out, nil
	}
	srcIDByKey, err := s.sourceIDsByKey(ctx, []string{sourceKeyDlsite})
	if err != nil {
		return nil, err
	}
	dlsiteSrc := srcIDByKey[sourceKeyDlsite]
	if err := s.bridgeGalgamePopularity(ctx, galgameIDs, galgameToWork, dlsiteSrc, out); err != nil {
		return nil, err
	}
	// The claimed works' catalog-native lane: native rows minus the
	// bridge-exclusive dlsite source, then a (source_id, metric) re-sort of
	// the union.
	claimedWorkIDs := make([]int64, 0, len(galgameToWork))
	for _, workID := range galgameToWork {
		claimedWorkIDs = append(claimedWorkIDs, workID)
	}
	if err := s.nativeWorkPopularity(ctx, claimedWorkIDs, dlsiteSrc, out); err != nil {
		return nil, err
	}
	for _, workID := range claimedWorkIDs {
		rows := out[workID]
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].SourceID != rows[j].SourceID {
				return rows[i].SourceID < rows[j].SourceID
			}
			return rows[i].Metric < rows[j].Metric
		})
	}
	return out, nil
}

// bridgeGalgamePopularity reads the claimed works' galgame_dlsite_meta rows in
// ONE query and pivots the three counter columns to metric rows (the dlsite
// lane; the caller re-sorts each claimed work after merging the catalog-native
// lane).
func (s *ReadService) bridgeGalgamePopularity(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, dlsiteSrc int16, out map[int64][]WorkPopularityRow) error {
	var rows []struct {
		GalgameID     int64  `gorm:"column:galgame_id"`
		DlCount       *int64 `gorm:"column:dl_count"`
		WishlistCount *int64 `gorm:"column:wishlist_count"`
		ReviewCount   *int64 `gorm:"column:review_count"`
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT galgame_id, dl_count, wishlist_count, review_count
		FROM galgame_dlsite_meta WHERE galgame_id IN ? ORDER BY galgame_id`, galgameIDs).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		workID, ok := galgameToWork[r.GalgameID]
		if !ok {
			continue
		}
		for _, pm := range []struct {
			metric int16
			value  *int64
		}{
			{model.PopularityMetricDownloads, r.DlCount},
			{model.PopularityMetricWishlist, r.WishlistCount},
			{model.PopularityMetricReviews, r.ReviewCount},
		} {
			if pm.value == nil { // unpublished counter — never a fake 0 row
				continue
			}
			out[workID] = append(out[workID], WorkPopularityRow{
				SourceID: dlsiteSrc, Metric: pm.metric, Value: *pm.value,
			})
		}
	}
	return nil
}

// nativeWorkPopularity reads works' catalog_work_popularity rows in ONE query,
// ordered so each work's rows are (source_id, metric)-ascending. excludeSrc>0
// drops that source's rows — the claimed lane excludes the bridge-exclusive
// dlsite source (shadowed native dlsite rows never surface).
func (s *ReadService) nativeWorkPopularity(ctx context.Context, workIDs []int64, excludeSrc int16, out map[int64][]WorkPopularityRow) error {
	var rows []struct {
		WorkID   int64 `gorm:"column:work_id"`
		SourceID int16 `gorm:"column:source_id"`
		Metric   int16 `gorm:"column:metric"`
		Value    int64 `gorm:"column:value"`
	}
	q := s.db.WithContext(ctx)
	if excludeSrc > 0 {
		q = q.Raw(`SELECT work_id, source_id, metric, value FROM catalog_work_popularity
		WHERE work_id IN ? AND source_id <> ? ORDER BY work_id, source_id, metric`, workIDs, excludeSrc)
	} else {
		q = q.Raw(`SELECT work_id, source_id, metric, value FROM catalog_work_popularity
		WHERE work_id IN ? ORDER BY work_id, source_id, metric`, workIDs)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkPopularityRow{
			SourceID: r.SourceID, Metric: r.Metric, Value: r.Value,
		})
	}
	return nil
}
