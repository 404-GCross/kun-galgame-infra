package wikirescue

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"
)

// sourceKeyDlsite / sourceKeyErogamespace are the two rating sources step t
// adopts that mediaSourceKey (a MEDIA mapping) does not know.
const (
	sourceKeyDlsite       = "dlsite"
	sourceKeyErogamespace = "erogamespace"
)

// popKey identifies one catalog_work_popularity row: uq_catalog_work_popularity
// (work_id, source_id, metric).
type popKey struct {
	workID int64
	srcID  int16
	metric int16
}

// popVal is the counter step t owns on a popularity row.
type popVal struct{ value int64 }

// stepMetaAdopt is the ONE-SHOT adoption of the three source-owned rating lanes
// and the DLsite popularity lane out of the wiki meta tables (step t,
// refs/proj/140 §2.5).
//
// It is deliberately NOT part of the daily follow-up chain, and the W1 final
// sweep must not run it either. The reason is ownership, not cost: this same
// commit removes the claim guards from the workratings / dlsitegenres importers,
// so from now on bangumi / dlsite / erogamespace rating + popularity rows on a
// CLAIMED work belong to those importers exactly as they already do on a bodyless
// one. They read the live staging mirrors; galgame_bangumi_meta and friends are
// snapshots of an older sync. Re-running t later would overwrite fresher importer
// values with staler wiki ones — a regression dressed as idempotency.
//
// So t exists for one purpose: to put the rows in place BEFORE the read face
// stops bridging them, in the window where the importers have not yet run over
// the newly-unguarded claimed population. Hence fill + update, never delete: a
// row t does not justify is the importers' business, not the mirror's.
//
// Bridge semantics, verbatim per lane (service.bridgeGalgameRatings /
// bridgeGalgamePopularity):
//
//   - bangumi: score > 0 (Bangumi's mean is > 0 as soon as one vote exists, so
//     this is "has ratings"); score as-is on the 0-10 scale, vote_count = total
//     (the summed score_details buckets), rank only when rank > 0 (0 = unranked,
//     ranks are 1-based) and NULL otherwise;
//   - dlsite rating: rate_average_star IS NOT NULL AND rate_count > 0; score is
//     DLsite's own displayed 0-5 star average, rank NULL (DLsite has no rank);
//   - erogamespace: median IS NOT NULL (NULL vs a real low 0 stay distinct);
//     score = median on the 0-100 scale, rank NULL;
//   - dlsite popularity: the three counter columns pivot to metrics 0/1/2. A NULL
//     counter yields NO row (DLsite genuinely does not publish dl_count for
//     commercial works — absent ≠ 0) while a published 0 DOES (a real value).
//
// Scores are never normalized across sources (0-10 mean vs 0-5 star vs 0-100
// median are different semantics); see ratingmirror.go for the encoding rule that
// makes the stored value reproduce the bridge's float64 exactly.
//
// OWNERSHIP SCOPE — (claimed work, source ∈ {bangumi, dlsite, erogamespace}) for
// ratings and (claimed work, source=dlsite) for popularity. The BANGUMI
// popularity rows on claimed works (the five favorite shelves, metrics 10-14) are
// the T2b importer's property and are outside the scope entirely.
func (r *Runner) stepMetaAdopt(ctx context.Context) (Stats, error) {
	st := Stats{Step: "t"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}
	db := r.catalog.WithContext(ctx)
	bgmSrc, err := resolveSourceID(db, mediaSourceBangumi)
	if err != nil {
		return st, err
	}
	dlSrc, err := resolveSourceID(db, sourceKeyDlsite)
	if err != nil {
		return st, err
	}
	egSrc, err := resolveSourceID(db, sourceKeyErogamespace)
	if err != nil {
		return st, err
	}
	desired := newMirrorDesired[ratingKey, ratingVal](0)

	// ── bangumi lane.
	var bgm []struct {
		GalgameID int64   `gorm:"column:galgame_id"`
		Score     float64 `gorm:"column:score"`
		Rank      int     `gorm:"column:rank"`
		Total     int     `gorm:"column:total"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, score, rank, total FROM galgame_bangumi_meta
		 WHERE score > 0 ORDER BY galgame_id`).Scan(&bgm).Error; err != nil {
		return st, fmt.Errorf("read galgame_bangumi_meta: %w", err)
	}
	for _, m := range bgm {
		workID, ok := span.byGalgame[m.GalgameID]
		if !ok {
			continue
		}
		v := ratingVal{score: m.Score, voteCount: m.Total}
		if m.Rank > 0 { // rank 0 = unranked on Bangumi (ranks are 1-based)
			rank := m.Rank
			v.rank = &rank
		}
		desired.put(ratingKey{workID: workID, srcID: bgmSrc}, v)
	}
	st.Source += len(bgm)

	// ── dlsite rating + popularity lanes (one read, two facets).
	var dl []struct {
		GalgameID     int64    `gorm:"column:galgame_id"`
		Star          *float64 `gorm:"column:rate_average_star"`
		RateCount     int      `gorm:"column:rate_count"`
		DlCount       *int64   `gorm:"column:dl_count"`
		WishlistCount *int64   `gorm:"column:wishlist_count"`
		ReviewCount   *int64   `gorm:"column:review_count"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, rate_average_star, coalesce(rate_count, 0) AS rate_count,
			dl_count, wishlist_count, review_count
		 FROM galgame_dlsite_meta ORDER BY galgame_id`).Scan(&dl).Error; err != nil {
		return st, fmt.Errorf("read galgame_dlsite_meta: %w", err)
	}
	desiredPop := newMirrorDesired[popKey, popVal](0)
	for _, m := range dl {
		workID, ok := span.byGalgame[m.GalgameID]
		if !ok {
			continue
		}
		if m.Star != nil && m.RateCount > 0 {
			desired.put(ratingKey{workID: workID, srcID: dlSrc}, ratingVal{score: *m.Star, voteCount: m.RateCount})
			st.Source++
		}
		for _, pm := range []struct {
			metric int16
			value  *int64
		}{
			{model.PopularityMetricDownloads, m.DlCount},
			{model.PopularityMetricWishlist, m.WishlistCount},
			{model.PopularityMetricReviews, m.ReviewCount},
		} {
			if pm.value == nil { // unpublished counter — never a fake 0 row
				continue
			}
			desiredPop.put(popKey{workID: workID, srcID: dlSrc, metric: pm.metric}, popVal{value: *pm.value})
			st.Source++
		}
	}

	// ── erogamespace lane.
	var eg []struct {
		GalgameID int64 `gorm:"column:galgame_id"`
		Median    int   `gorm:"column:median"`
		VoteCount int   `gorm:"column:vote_count"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, median, vote_count FROM galgame_eg_meta
		 WHERE median IS NOT NULL ORDER BY galgame_id`).Scan(&eg).Error; err != nil {
		return st, fmt.Errorf("read galgame_eg_meta: %w", err)
	}
	for _, m := range eg {
		workID, ok := span.byGalgame[m.GalgameID]
		if !ok {
			continue
		}
		desired.put(ratingKey{workID: workID, srcID: egSrc}, ratingVal{score: float64(m.Median), voteCount: m.VoteCount})
	}
	st.Source += len(eg)
	st.Anchored = desired.len() + desiredPop.len()

	ratingExisting, err := r.loadRatingScope(ctx, span, []int16{bgmSrc, dlSrc, egSrc})
	if err != nil {
		return st, err
	}
	ratingSpec := r.ratingSpec()
	ratingSpec.keepExtra = true
	ledger := newMirrorLedger()
	if err := runMirror(ctx, r, ledger, ratingSpec, desired, ratingExisting); err != nil {
		return st, err
	}
	ratingTouched := len(ledger.touched)

	var popScope []struct {
		ID       int64 `gorm:"column:id"`
		WorkID   int64 `gorm:"column:work_id"`
		SourceID int16 `gorm:"column:source_id"`
		Metric   int16 `gorm:"column:metric"`
		Value    int64 `gorm:"column:value"`
	}
	if err := db.Raw(
		`SELECT t.id, t.work_id, t.source_id, t.metric, t.value FROM catalog_work_popularity t
		 `+claimedScopeJoin+` WHERE t.source_id = ?`, siteGalgameWiki, dlSrc).Scan(&popScope).Error; err != nil {
		return st, fmt.Errorf("read catalog_work_popularity scope: %w", err)
	}
	popExisting := make(map[popKey]mirrorExisting[popVal], len(popScope))
	for _, x := range popScope {
		popExisting[popKey{workID: x.WorkID, srcID: x.SourceID, metric: x.Metric}] =
			mirrorExisting[popVal]{id: x.ID, val: popVal{value: x.Value}}
	}
	popSpec := mirrorSpec[popKey, popVal]{
		table:      "catalog_work_popularity",
		insertCols: []string{"work_id", "source_id", "metric", "value", "created_at", "updated_at"},
		rowOf: func(k popKey, v popVal) []any {
			return []any{k.workID, k.srcID, k.metric, v.value, r.now, r.now}
		},
		updateCols: []string{"value", "updated_at"},
		updateArgs: func(v popVal) []any { return []any{v.value, r.now} },
		workOf:     func(k popKey) int64 { return k.workID },
		same:       func(a, b popVal) bool { return a == b },
		keepExtra:  true,
	}
	// The two facets share ONE step ledger, so Touched is the distinct union of
	// the works both arms bumped; the Note carries the per-facet split.
	if err := runMirror(ctx, r, ledger, popSpec, desiredPop, popExisting); err != nil {
		return st, err
	}
	ledger.into(&st)
	st.Note = fmt.Sprintf("one-shot adoption, NOT in the daily chain nor in the W1 final sweep; ratings planned=%d (works %d), popularity planned=%d",
		desired.len(), ratingTouched, desiredPop.len())
	return st, nil
}
