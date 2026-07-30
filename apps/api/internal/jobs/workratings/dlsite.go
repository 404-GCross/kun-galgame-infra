package workratings

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// The DLsite lane (step 62): one candidate pass feeds BOTH facets — the rating
// row (catalog_work_rating, source=dlsite) and the popularity rows
// (catalog_work_popularity, one per published counter). DLsite worknos anchor
// at RELEASE level (SKU-natured, doc 17 R3), so the candidate query walks
// work → release → external_ref — unlike the work-level bgm/eg lanes — and
// pins medium=galgame (the game-domain ruling: the ASMR family carries DLsite
// anchors too and is out of scope).

// dlsiteCandidate is one galgame work with its representative DLsite workno.
// DISTINCT ON keeps ONE anchor per work (the lexicographically lowest workno —
// the bgm-lane precedent; surveyed live: zero galgame-medium works carry more than
// one distinct workno).
type dlsiteCandidate struct {
	WorkID int64  `gorm:"column:work_id"`
	Workno string `gorm:"column:workno"`
}

// loadDlsiteCandidates resolves GALGAME-medium works carrying a DLsite EXACT
// release anchor. Limit/Offset window the distinct-work list in Go (the
// dlsitemedia chunking discipline).
func loadDlsiteCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]dlsiteCandidate, error) {
	var out []dlsiteCandidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id AS workno
			FROM catalog_work w
			JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
				AND r.source_id = ? AND r.link_kind = ?
			WHERE w.medium_id = ? AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeRelease, reg.dlsiteSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// dlsiteData is one DLsite mirror works row, extracted from info_json
// (surveyed 2026-07-21: info_json carries all five values on every mirror row;
// product_json has the star but none of the counters). All fields nullable —
// `->>` yields SQL NULL for absent keys AND JSON nulls, exactly the "DLsite
// does not publish this counter" semantics. rateStar is the mirror's
// rate_average_2dp: DLsite's own displayed star average on the native 0-5
// scale (the wire key rate_average_star is that value half-star-bucketed ×10 —
// a widget encoding, not stored).
type dlsiteData struct {
	rateStar  *float64
	rateCount *int
	dlCount   *int64
	wishlist  *int64
	reviews   *int
}

// loadDlsiteMirror batch-loads the referenced mirror rows. Negative counters
// (one corrupt row observed live) and a vote-less star (none observed — star ⟺
// rate_count>=5 in the survey) normalize to NULL: never a fake value.
func loadDlsiteMirror(ctx context.Context, dlsiteDB *gorm.DB, worknos []string) (map[string]dlsiteData, error) {
	out := map[string]dlsiteData{}
	type row struct {
		Workno    string   `gorm:"column:workno"`
		RateStar  *float64 `gorm:"column:rate_star"`
		RateCount *int     `gorm:"column:rate_count"`
		DlCount   *int64   `gorm:"column:dl_count"`
		Wishlist  *int64   `gorm:"column:wishlist_count"`
		Reviews   *int     `gorm:"column:review_count"`
	}
	for start := 0; start < len(worknos); start += 1000 {
		end := min(start+1000, len(worknos))
		var batch []row
		if err := dlsiteDB.WithContext(ctx).Table("works").
			Select(`workno,
				(info_json->>'rate_average_2dp')::float8 AS rate_star,
				(info_json->>'rate_count')::int          AS rate_count,
				(info_json->>'dl_count')::bigint         AS dl_count,
				(info_json->>'wishlist_count')::bigint   AS wishlist_count,
				(info_json->>'review_count')::int        AS review_count`).
			Where("workno IN ?", worknos[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			d := dlsiteData{
				rateStar:  r.RateStar,
				rateCount: dropNeg(r.RateCount),
				dlCount:   dropNeg(r.DlCount),
				wishlist:  dropNeg(r.Wishlist),
				reviews:   dropNeg(r.Reviews),
			}
			if d.rateCount == nil || *d.rateCount <= 0 {
				d.rateStar, d.rateCount = nil, nil // the pair lives and dies together
			}
			out[r.Workno] = d
		}
	}
	return out, nil
}

// dropNeg normalizes a corrupt negative counter to NULL.
func dropNeg[T int | int64](p *T) *T {
	if p != nil && *p < 0 {
		return nil
	}
	return p
}

// runDlsiteLane decides and (apply) writes the dlsite rating + popularity rows.
func runDlsiteLane(ctx context.Context, db, dlsiteDB *gorm.DB, w *writer, reg registry, opts Opts) error {
	cands, err := loadDlsiteCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load DLsite candidates: %w", err)
	}
	st := w.stats
	st.DlCandidates = len(cands)

	worknos := make([]string, 0, len(cands))
	for _, c := range cands {
		worknos = append(worknos, c.Workno)
	}
	mirror, err := loadDlsiteMirror(ctx, dlsiteDB, worknos)
	if err != nil {
		return fmt.Errorf("load DLsite mirror works: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dl, ok := mirror[c.Workno]
		if !ok {
			st.DlMissingMirror++
			continue
		}

		// Rating row: only when DLsite publishes one (never a fake zero).
		if dl.rateStar != nil {
			st.DlRatingPlanned++
			collect(&st.DlSamples, Sample{WorkID: c.WorkID, Workno: c.Workno, Score: *dl.rateStar, VoteCount: *dl.rateCount})
			w.write(ctx, plannedRow{
				WorkID: c.WorkID, SourceID: reg.dlsiteSource,
				Score: *dl.rateStar, VoteCount: *dl.rateCount,
			}, opts.Apply, &st.DlRatingWritten, &st.DlRatingUnchanged)
		} else {
			st.DlNoRating++
		}

		// Popularity rows: one per PUBLISHED counter, metric-ascending. An
		// absent counter never becomes a row (NULL ≠ 0); a published 0 does
		// (it is a real value the refresh loop keeps current).
		for _, pm := range []struct {
			metric int16
			value  *int64
		}{
			{model.PopularityMetricDownloads, dl.dlCount},
			{model.PopularityMetricWishlist, dl.wishlist},
			{model.PopularityMetricReviews, intPtrTo64(dl.reviews)},
		} {
			if pm.value == nil {
				continue
			}
			st.PopPlanned++
			w.writePopularity(ctx, popPlannedRow{
				WorkID: c.WorkID, SourceID: reg.dlsiteSource,
				Metric: pm.metric, Value: *pm.value,
			}, opts.Apply)
		}
	}
	return nil
}

func intPtrTo64(p *int) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}
