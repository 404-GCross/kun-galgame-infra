// public_series.go — the series detail endpoint (GET /v1/catalog/series/{id},
// wave 149c) over the step-94 grouping entity (catalog_series +
// catalog_series_member) and the intro facet the data-layer-retirement wave
// rescued into catalog_series_intro.
//
// It closes the aggregation track's last read gap: series membership was
// already a works-list FILTER (works?series_id=) and an inline block on the
// work record, but the series itself had no address — so a consumer holding a
// series id could not render its name, its intro or (in one call) its members.
package service

import (
	"context"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// SeriesDetail projects one series. found=false → 404 on an unknown id.
//
// Series carry NO merge/soft-delete machinery (no catalog_redirect entity type,
// no deleted_at column): the importer mirrors the source, inserting and
// deleting membership rows, so a miss here is a plain miss — deliberately NOT
// routed through the redirect helper, which would answer 301 for an id that was
// never merged anywhere.
//
// include=works attaches the member works (LIVE galgame fetchable set, offset
// paging, r18 briefs dropped unless nsfw) — the labels/{id} and tags/{id}
// works convention verbatim.
func (s *PublicService) SeriesDetail(ctx context.Context, id int64, withWorks, nsfw bool, limit, offset int) (dto.PublicSeriesDetail, bool, error) {
	var head struct {
		ID          int64  `gorm:"column:id"`
		DisplayName string `gorm:"column:display_name"`
		SourceID    int16  `gorm:"column:source_id"`
		ExternalID  string `gorm:"column:external_id"`
	}
	if err := s.db.WithContext(ctx).Raw(
		`SELECT id, display_name, source_id, external_id FROM catalog_series WHERE id = ?`, id).
		Scan(&head).Error; err != nil {
		return dto.PublicSeriesDetail{}, false, err
	}
	if head.ID == 0 {
		return dto.PublicSeriesDetail{}, false, nil // identity PK starts at 1 → 0 = miss
	}
	rec := dto.PublicSeriesDetail{ID: head.ID, DisplayName: head.DisplayName}
	// A series anchors in its source IN-ROW (the unique (source_id, external_id)
	// key) rather than through catalog_external_ref, so refs[] is built from
	// that key — same {source, external_id} shape the other identity faces
	// publish, and exact by construction (a source series id is the source's own
	// identifier, never a guess).
	rec.Refs = []dto.PublicCatalogRef{{Source: s.sourceKey(head.SourceID), ExternalID: head.ExternalID}}
	intros, err := s.seriesIntros(ctx, id)
	if err != nil {
		return dto.PublicSeriesDetail{}, false, err
	}
	rec.Intros = intros
	if withWorks {
		var wrows []struct {
			WorkID int64 `gorm:"column:work_id"`
		}
		if err := s.db.WithContext(ctx).Raw(`
			SELECT m.work_id FROM catalog_series_member m
			JOIN catalog_work w ON w.id = m.work_id AND w.deleted_at IS NULL AND w.status = ? AND w.medium_id = ?
			WHERE m.series_id = ?
			ORDER BY m.work_id
			LIMIT ? OFFSET ?`,
			model.WorkStatusLive, galgameMediumID, id, limit, offset).Scan(&wrows).Error; err != nil {
			return dto.PublicSeriesDetail{}, false, err
		}
		ids := make([]int64, len(wrows))
		for i, r := range wrows {
			ids[i] = r.WorkID
		}
		briefs, err := s.loadWorkBriefs(ctx, ids, nsfw)
		if err != nil {
			return dto.PublicSeriesDetail{}, false, err
		}
		rec.Works = make([]dto.PublicWorkBrief, 0, len(ids))
		for _, wid := range ids {
			if b := briefs[wid]; b != nil {
				rec.Works = append(rec.Works, *b)
			}
		}
		rec.NextOffset = nextOffset(len(wrows), limit, offset)
	}
	return rec, true, nil
}

// seriesIntros loads a series' descriptions, ordered (lang, source_id). source
// goes through sourceKey so the wire carries the PUBLIC source spelling — the
// label / tag / character intro convention.
//
// Unlike those, this list is NOT collapsed to one row per language: a series
// intro is hand-written user content the retirement wave rescued with no
// upstream to regenerate it from, so a second source's body for the same
// language is a second body, not a worse copy of the first — dropping it would
// silently hide content that exists nowhere else. Empty → [].
func (s *PublicService) seriesIntros(ctx context.Context, seriesID int64) ([]dto.PublicSeriesIntro, error) {
	var rows []struct {
		Lang     string `gorm:"column:lang"`
		Intro    string `gorm:"column:intro"`
		SourceID int16  `gorm:"column:source_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT lang, intro, source_id FROM catalog_series_intro
		WHERE series_id = ? ORDER BY lang, source_id`, seriesID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.PublicSeriesIntro, 0, len(rows))
	for _, r := range rows {
		out = append(out, dto.PublicSeriesIntro{Lang: r.Lang, Intro: r.Intro, Source: s.sourceKey(r.SourceID)})
	}
	return out, nil
}
