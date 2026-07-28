// public_works_list.go — the works browse lane (GET /v1/catalog/works) and
// the changes feed (GET /v1/catalog/changes), doc 106 G1/G2.
//
// OWNERSHIP (doc 106 W2 file-ownership table): the query builders
// (WorksList / Changes) are the W2A wave's to implement — W1 ships them as
// compiling stubs (empty page) so the routes/spec land first. The enrichment
// helpers below the marker are W1-owned and FROZEN: W2A calls them, never
// edits them.
//
// Population invariant (both lanes): the public fetchable set — galgame
// medium, status=live, not soft-deleted — exactly the wave-105 works-index
// population. The works LIST additionally drops r18 rows unless nsfw; the
// CHANGES feed does NOT nsfw-gate (ids + timestamps are identity, not
// content: the consumer's detail follow-up re-applies the gate, mirroring
// /v1/galgame/changes and the redirects feed).
package service

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/dto"
)

// WorksListFilter is the works browse lane's filter set (all optional; zero
// value = the whole fetchable set). Date bounds are composed ordinals
// (y*10000 + m*100 + d) over the EARLIEST release date per work.
type WorksListFilter struct {
	ContentRating *int16 // model.ContentRating* — nil = all (r18 still needs NSFW)
	Claimed       *bool  // true = claimed only; false = bodyless only; nil = both
	LabelID       int64  // via catalog_work_label
	TagID         int64  // canonical tag id, via catalog_tag_source_map ⋈ catalog_work_tag
	SeriesID      int64  // via catalog_series_member
	Platform      string // release-level platform ∪ work-level platform rows
	ReleasedAfter int64  // composed ordinal, inclusive; 0 = unbounded
	ReleasedBefor int64  // composed ordinal, inclusive; 0 = unbounded
	IDs           []int64
	NSFW          bool
	Sort          string // "id" (default, ASC) | "updated" (DESC, newest first)
}

// WorksList serves the keyset works browse lane. Returns one page of enriched
// list items plus the next cursor (nil on the last page).
//
// W2A TODO: build the filtered keyset query over catalog_work (population
// invariant above; sort=id → (id) ASC, sort=updated → (updated_at, id) DESC
// riding idx_catalog_work_updated_id), map rows through enrichWorkListItems,
// and mint the next cursor from the last row. ErrBadCursor on a bad cursor.
func (s *PublicService) WorksList(ctx context.Context, f WorksListFilter, cursor string, limit int) (dto.PublicWorksListData, error) {
	if _, err := decodePublicCursor(cursor, worksSortLane(f.Sort)); err != nil {
		return dto.PublicWorksListData{}, err
	}
	_ = ctx
	_ = limit
	return dto.PublicWorksListData{Items: []dto.PublicWorkListItem{}}, nil
}

// Changes serves the incremental works changes feed: LIVE galgame works
// ordered by (updated_at, id) ASC, resuming from the cursor. next_cursor is
// ALWAYS returned (it advances past the last row even on a short page).
//
// W2A TODO: implement the keyset scan (strictly-greater (updated_at, id) row
// comparison over idx_catalog_work_updated_id, RFC3339Nano watermarks in the
// cursor) and echo the inbound cursor back when the page is empty.
func (s *PublicService) Changes(ctx context.Context, cursor string, limit int) (dto.PublicChangesData, error) {
	cur, err := decodePublicCursor(cursor, "changes")
	if err != nil {
		return dto.PublicChangesData{}, err
	}
	_ = ctx
	_ = limit
	return dto.PublicChangesData{
		Items:      []dto.PublicChangeItem{},
		NextCursor: encodePublicCursor(cur),
	}, nil
}

// worksSortLane normalizes the works-list sort token to its cursor lane.
func worksSortLane(sort string) string {
	if sort == "updated" {
		return "updated"
	}
	return "id"
}

// ── W1-frozen enrichment helpers below (W2A calls, never edits) ──────────────

// workListSourceRow is what the W2A page query produces per work — the raw
// registry columns enrichWorkListItems needs.
type workListSourceRow struct {
	ID            int64
	MediumID      int16
	DisplayName   string
	OLang         string
	ContentRating int16
	Site          *string
	ProductWorkID *int64
	UpdatedAt     string // RFC3339Nano, rendered by the query (to_char / time.Time formatting)
}

// enrichWorkListItems projects one page of registry rows to the public list
// items, batch-attaching release_date (earliest release, partial ISO) and one
// representative cover (portrait pin first; sexual-flagged covers are never
// served to sfw callers). Order is preserved.
func (s *PublicService) enrichWorkListItems(ctx context.Context, rows []workListSourceRow, nsfw bool) ([]dto.PublicWorkListItem, error) {
	if len(rows) == 0 {
		return []dto.PublicWorkListItem{}, nil
	}
	ids := make([]int64, len(rows))
	subjects := make([]claimSubject, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		subjects[i] = claimSubject{WorkID: r.ID, Site: r.Site, ProductWorkID: r.ProductWorkID}
	}
	dates, err := s.earliestReleaseDatesFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	covers, err := s.read.loadWorkCovers(ctx, subjects)
	if err != nil {
		return nil, err
	}
	out := make([]dto.PublicWorkListItem, len(rows))
	for i, r := range rows {
		out[i] = dto.PublicWorkListItem{
			ID: r.ID, Medium: s.mediumKey(r.MediumID), DisplayName: r.DisplayName,
			ContentRating: contentRatingKey(r.ContentRating), OLang: r.OLang,
			ReleaseDate: dates[r.ID], ClaimedBy: claimedBy(r.Site, r.ProductWorkID),
			Cover: s.pickListCover(covers[r.ID], nsfw), Updated: r.UpdatedAt,
		}
	}
	return out, nil
}

// earliestReleaseDatesFor batch-loads each work's earliest release date as a
// partial ISO string (YYYY[-MM[-DD]]), keyed by work id; works with no dated
// release have no entry (nil pointer on the item).
func (s *PublicService) earliestReleaseDatesFor(ctx context.Context, ids []int64) (map[int64]*string, error) {
	if len(ids) == 0 {
		return map[int64]*string{}, nil
	}
	var rows []struct {
		WorkID int64 `gorm:"column:work_id"`
		Ord    int64 `gorm:"column:ord"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT work_id,
		       min(released_y::int * 10000 + coalesce(released_m,0)::int * 100 + coalesce(released_d,0)::int) AS ord
		FROM catalog_release
		WHERE work_id IN ? AND released_y IS NOT NULL AND deleted_at IS NULL
		GROUP BY work_id`, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]*string, len(rows))
	for _, r := range rows {
		d := partialISOFromOrdinal(r.Ord)
		out[r.WorkID] = &d
	}
	return out, nil
}

// partialISOFromOrdinal renders a composed date ordinal (y*10000+m*100+d,
// month/day 0 = unknown) as YYYY[-MM[-DD]].
func partialISOFromOrdinal(ord int64) string {
	y, m, d := ord/10000, (ord/100)%100, ord%100
	out := fmt.Sprintf("%04d", y)
	if m > 0 {
		out = fmt.Sprintf("%s-%02d", out, m)
		if d > 0 {
			out = fmt.Sprintf("%s-%02d", out, d)
		}
	}
	return out
}

// pickListCover picks one representative cover URL for a list item: portrait
// pin first, then the loader's (sort_order, image_hash) order. An sfw caller
// never receives a sexual-flagged cover ("" when none qualifies).
func (s *PublicService) pickListCover(rows []WorkCoverRow, nsfw bool) string {
	var fallback string
	for _, c := range rows {
		if !nsfw && c.Sexual != 0 {
			continue
		}
		url := s.imageURL(c.ImageHash)
		if url == "" {
			continue
		}
		if c.PortraitPinned {
			return url
		}
		if fallback == "" {
			fallback = url
		}
	}
	return fallback
}
