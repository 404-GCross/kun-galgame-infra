package service

import (
	"context"

	"api/internal/platform/catalog/model"
)

// Popularity read face (step 62, the ratings facet's companion) — the sixth
// media-aggregation facet, structurally identical to ratings/tags: CLAIMED
// works bridge, BODYLESS works read native rows, strict XOR, source_id on
// every row. The claimed bridge reads ONE narrow meta table today
// (galgame_dlsite_meta — the step-62 product co-located in kun_catalog); a
// future source (e.g. the Bangumi favorite buckets) adds a lane here plus
// PopularityMetric* constants, nothing else.
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
// (source, metric) — never summed across sources (a DLsite sale and a future
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
// the media-aggregation contract (refs/proj/51 §2/§3/§8, step 62):
//
//   - CLAIMED (site='galgame_wiki'): bridge from galgame_dlsite_meta (see the
//     file doc for the mapping). Bridge-not-copy (§2): meta rows are never
//     materialized into catalog_work_popularity.
//   - BODYLESS (site=”/NULL): the work's catalog_work_popularity rows.
//   - Strict XOR (§8.D): a claimed work reads ONLY the bridge; it never falls
//     back to native rows even if it still has shadowed ones (shadow-never-delete).
//
// Batched (§9.1): claimed works bridge in one galgame_dlsite_meta query,
// bodyless works read in one catalog_work_popularity query — never per-work.
// Each work's rows are ordered (source_id, metric) ascending. Returns a map
// keyed by work id; a work with no popularity is absent (the caller renders []).
func (s *ReadService) loadWorkPopularity(ctx context.Context, subjects []claimSubject) (map[int64][]WorkPopularityRow, error) {
	out := make(map[int64][]WorkPopularityRow, len(subjects))
	galgameIDs, galgameToWork, bodylessIDs := partitionClaimSubjects(subjects)
	if len(galgameIDs) > 0 {
		if err := s.bridgeGalgamePopularity(ctx, galgameIDs, galgameToWork, out); err != nil {
			return nil, err
		}
	}
	if len(bodylessIDs) > 0 {
		if err := s.nativeWorkPopularity(ctx, bodylessIDs, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// bridgeGalgamePopularity reads the claimed works' galgame_dlsite_meta rows in
// ONE query and pivots the three counter columns to metric rows
// (metric-ascending; today's single-source bridge makes that (source_id,
// metric)-ascending for free).
func (s *ReadService) bridgeGalgamePopularity(ctx context.Context, galgameIDs []int64, galgameToWork map[int64]int64, out map[int64][]WorkPopularityRow) error {
	srcIDByKey, err := s.sourceIDsByKey(ctx, []string{sourceKeyDlsite})
	if err != nil {
		return err
	}
	dlsiteSrc := srcIDByKey[sourceKeyDlsite]

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

// nativeWorkPopularity reads the bodyless works' catalog_work_popularity rows
// in ONE query, ordered so each work's rows are (source_id, metric)-ascending.
func (s *ReadService) nativeWorkPopularity(ctx context.Context, workIDs []int64, out map[int64][]WorkPopularityRow) error {
	var rows []struct {
		WorkID   int64 `gorm:"column:work_id"`
		SourceID int16 `gorm:"column:source_id"`
		Metric   int16 `gorm:"column:metric"`
		Value    int64 `gorm:"column:value"`
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT work_id, source_id, metric, value FROM catalog_work_popularity
		WHERE work_id IN ? ORDER BY work_id, source_id, metric`, workIDs).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkPopularityRow{
			SourceID: r.SourceID, Metric: r.Metric, Value: r.Value,
		})
	}
	return nil
}
