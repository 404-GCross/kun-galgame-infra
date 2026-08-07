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
	// Unconditional, not include-gated: the caller needs to know the series
	// leads to adult works BEFORE deciding whether to ask for them.
	_, nsfwWorks, err := s.workCountsWithNSFW(ctx, seriesWorkEdge, []int64{id}, nsfw)
	if err != nil {
		return dto.PublicSeriesDetail{}, false, err
	}
	rec.HasNSFW = nsfwWorks[id] > 0
	if withWorks {
		var wrows []struct {
			WorkID   int64 `gorm:"column:work_id"`
			Position int16 `gorm:"column:position"`
			Kind     int16 `gorm:"column:kind"`
		}
		// Reading order first (wave 184): position is 1-based, and 0 is the "not
		// ordered yet" sentinel — those rows sort LAST rather than first, so a
		// half-backfilled series shows what it does know before what it does not.
		// work_id still breaks every tie, so the page boundary stays stable.
		if err := s.db.WithContext(ctx).Raw(`
			SELECT m.work_id, m.position, m.kind FROM catalog_series_member m
			JOIN catalog_work w ON w.id = m.work_id AND w.deleted_at IS NULL AND w.status = ? AND w.medium_id = ?
			WHERE m.series_id = ?
			ORDER BY (m.position = 0), m.position, m.work_id
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
		// members[] runs PARALLEL to works[]: same rows, same order, same r18
		// drop — element i of one describes element i of the other. It is a
		// separate array rather than two more fields on PublicWorkBrief because
		// the brief is shared by every works-list face in this projection, and
		// position/kind are facts about a MEMBERSHIP, not about the work.
		rec.Members = make([]dto.PublicSeriesMember, 0, len(ids))
		for _, r := range wrows {
			b := briefs[r.WorkID]
			if b == nil {
				continue
			}
			rec.Works = append(rec.Works, *b)
			rec.Members = append(rec.Members, dto.PublicSeriesMember{
				WorkID: r.WorkID, Position: r.Position, Kind: seriesMemberKindKey(r.Kind),
			})
		}
		rec.NextOffset = nextOffset(len(wrows), limit, offset)
	}
	return rec, true, nil
}

// seriesMemberKindKey renders catalog_series_member.kind as the public face's
// string key — the same "never publish a numeric enum id" rule content_rating
// and source follow. An id this projection does not know renders as unknown
// rather than leaking the raw number.
func seriesMemberKindKey(kind int16) string {
	switch kind {
	case model.SeriesMemberKindMain:
		return "main"
	case model.SeriesMemberKindFandisc:
		return "fandisc"
	case model.SeriesMemberKindSideStory:
		return "side_story"
	case model.SeriesMemberKindCollection:
		return "collection"
	default:
		return "unknown"
	}
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

// SeriesList serves the keyset series browse lane (id ASC), the fourth member
// of the labels / tags / engines family and shaped identically to them.
//
// It exists because a series id had an ADDRESS but no way to be discovered: a
// picker or an index had to already know the id to render a name. Search is the
// wrong tool here — the facet is ~600 curated rows with no Meilisearch index of
// its own, and giving it one would mean a new index plus a reindex lane for a
// population two keyset pages cover.
//
// A malformed cursor is ErrBadCursor (the handler maps it to 400).
func (s *PublicService) SeriesList(ctx context.Context, nsfw bool, cursor string, limit int) (dto.PublicSeriesListData, error) {
	cur, err := decodePublicCursor(cursor, taxonomyLaneSeries)
	if err != nil {
		return dto.PublicSeriesListData{}, err
	}
	limit = clampBrowseLimit(limit)

	// No deleted_at predicate, unlike the other three lanes: catalog_series
	// carries no soft-delete column at all (see SeriesDetail — the importer
	// mirrors the source by inserting and deleting rows outright).
	var where []string
	var args []any
	if cur.ID > 0 {
		where = append(where, "s.id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit+taxonomyOverFetch)

	var rows []struct {
		ID          int64  `gorm:"column:id"`
		DisplayName string `gorm:"column:display_name"`
		Source      string `gorm:"column:source"`
	}
	q := `SELECT s.id, s.display_name, coalesce(src.key, '') AS source
		FROM catalog_series s
		LEFT JOIN catalog_source src ON src.id = s.source_id ` +
		whereClause(where) + ` ORDER BY s.id ASC LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicSeriesListData{}, err
	}

	rows, more := taxonomyTrim(rows, limit)
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	counts, nsfwWorks, err := s.workCountsWithNSFW(ctx, seriesWorkEdge, ids, nsfw)
	if err != nil {
		return dto.PublicSeriesListData{}, err
	}
	out := dto.PublicSeriesListData{Items: make([]dto.PublicSeriesListItem, len(rows))}
	for i, r := range rows {
		out.Items[i] = dto.PublicSeriesListItem{
			ID: r.ID, DisplayName: r.DisplayName, Source: r.Source,
			WorkCount: counts[r.ID], HasNSFW: nsfwWorks[r.ID] > 0,
		}
	}
	if out.Total, err = s.taxonomyTotal(ctx, "catalog_series", nil, nil); err != nil {
		return dto.PublicSeriesListData{}, err
	}
	out.NextCursor = taxonomyNextCursor(taxonomyLaneSeries, ids, more)
	return out, nil
}
