package workratings

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type dlsiteCandidate struct {
	WorkID int64  `gorm:"column:work_id"`
	Workno string `gorm:"column:workno"`
}

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

type dlsiteData struct {
	rateStar  *float64
	rateCount *int
	dlCount   *int64
	wishlist  *int64
	reviews   *int
}

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
				d.rateStar, d.rateCount = nil, nil
			}
			out[r.Workno] = d
		}
	}
	return out, nil
}

func dropNeg[T int | int64](p *T) *T {
	if p != nil && *p < 0 {
		return nil
	}
	return p
}

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
