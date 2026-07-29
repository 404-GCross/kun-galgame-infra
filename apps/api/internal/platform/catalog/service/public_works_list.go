// public_works_list.go — the works browse lane (GET /v1/catalog/works) and
// the changes feed (GET /v1/catalog/changes), doc 106 G1/G2.
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
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
)

// WorksListFilter is the works browse lane's filter set (all optional; zero
// value = the whole fetchable set). Date bounds are composed ordinals
// (y*10000 + m*100 + d) over the EARLIEST release date per work.
type WorksListFilter struct {
	ContentRating  *int16 // model.ContentRating* — nil = all (r18 still needs NSFW)
	Claimed        *bool  // true = claimed only; false = bodyless only; nil = both
	LabelID        int64  // via catalog_work_label
	TagID          int64  // canonical tag id, via catalog_tag_source_map ⋈ catalog_work_tag
	SeriesID       int64  // via catalog_series_member
	Platform       string // release-level platform ∪ work-level platform rows
	ReleasedAfter  int64  // composed ordinal, inclusive; 0 = unbounded
	ReleasedBefore int64  // composed ordinal, inclusive; 0 = unbounded
	IDs            []int64
	NSFW           bool
	Sort           string // "id" (default, ASC) | "updated" (DESC, newest first)
}

// WorksList serves the keyset works browse lane. Returns one page of enriched
// list items plus the next cursor (nil on the last page). A malformed cursor
// is ErrBadCursor (the handler maps it to 400).
func (s *PublicService) WorksList(ctx context.Context, f WorksListFilter, cursor string, limit int) (dto.PublicWorksListData, error) {
	lane := worksSortLane(f.Sort)
	cur, err := decodePublicCursor(cursor, lane)
	if err != nil {
		return dto.PublicWorksListData{}, err
	}
	// Defensive only — the handler is the wire authority (it 400s a bad limit).
	// Clamp at the ceiling rather than resetting to the default so both layers
	// agree on what an over-max limit means.
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}

	where := []string{"w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?"}
	args := []any{model.WorkStatusLive, galgameMediumID}

	if !f.NSFW {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	if f.ContentRating != nil {
		where = append(where, "w.content_rating = ?")
		args = append(args, *f.ContentRating)
	}
	if f.Claimed != nil {
		if *f.Claimed {
			where = append(where, "w.site <> ''") // NULL and '' both excluded (bodyless)
		} else {
			where = append(where, "(w.site = '' OR w.site IS NULL)")
		}
	}
	if f.LabelID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM catalog_work_label wl WHERE wl.work_id = w.id AND wl.label_id = ?)")
		args = append(args, f.LabelID)
	}
	if f.TagID > 0 {
		// Canonical tag → any source tag mapped to it (idx on catalog_work_tag
		// (work_id,...) unique carries the correlated work_id probe).
		where = append(where, `EXISTS (SELECT 1 FROM catalog_work_tag wt
			JOIN catalog_tag_source_map m ON m.source_id = wt.source_id AND m.source_name = wt.name
			WHERE wt.work_id = w.id AND m.tag_id = ?)`)
		args = append(args, f.TagID)
	}
	if f.SeriesID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM catalog_series_member sm WHERE sm.work_id = w.id AND sm.series_id = ?)")
		args = append(args, f.SeriesID)
	}
	if f.Platform != "" {
		// Release-level primary platform ∪ work-level platform rows (the two
		// grains a consumer unions on the detail face).
		where = append(where, `(EXISTS (SELECT 1 FROM catalog_release r WHERE r.work_id = w.id AND r.deleted_at IS NULL AND r.platform = ?)
			OR EXISTS (SELECT 1 FROM catalog_work_platform wp WHERE wp.work_id = w.id AND wp.platform = ?))`)
		args = append(args, f.Platform, f.Platform)
	}
	if f.ReleasedAfter > 0 || f.ReleasedBefore > 0 {
		earliest := `(SELECT min(r.released_y::int*10000 + coalesce(r.released_m,0)::int*100 + coalesce(r.released_d,0)::int)
			FROM catalog_release r WHERE r.work_id = w.id AND r.released_y IS NOT NULL AND r.deleted_at IS NULL)`
		if f.ReleasedAfter > 0 {
			where = append(where, earliest+" >= ?")
			args = append(args, f.ReleasedAfter)
		}
		if f.ReleasedBefore > 0 {
			where = append(where, earliest+" <= ?")
			args = append(args, f.ReleasedBefore)
		}
	}
	if len(f.IDs) > 0 {
		where = append(where, "w.id IN ?")
		args = append(args, f.IDs)
	}

	var order string
	if lane == "updated" {
		if cur.Updated != "" {
			ts, perr := time.Parse(time.RFC3339Nano, cur.Updated)
			if perr != nil {
				return dto.PublicWorksListData{}, ErrBadCursor
			}
			// Row-value comparison (not OR expansion) so the keyset lands as an
			// Index Cond on idx_catalog_work_updated_id instead of a Filter.
			where = append(where, "(w.updated_at, w.id) < (?, ?)")
			args = append(args, ts, cur.ID)
		}
		order = "ORDER BY w.updated_at DESC, w.id DESC"
	} else {
		if cur.ID > 0 {
			where = append(where, "w.id > ?")
			args = append(args, cur.ID)
		}
		order = "ORDER BY w.id ASC"
	}

	q := `SELECT w.id, w.medium_id, w.display_name, w.olang, w.content_rating, w.site, w.product_work_id, w.updated_at
		FROM catalog_work w WHERE ` + strings.Join(where, " AND ") + " " + order + " LIMIT ?"
	args = append(args, limit)

	var rows []struct {
		ID            int64
		MediumID      int16
		DisplayName   string
		OLang         string
		ContentRating int16
		Site          *string
		ProductWorkID *int64
		UpdatedAt     time.Time
	}
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicWorksListData{}, err
	}

	src := make([]workListSourceRow, len(rows))
	for i, r := range rows {
		src[i] = workListSourceRow{
			ID: r.ID, MediumID: r.MediumID, DisplayName: r.DisplayName, OLang: r.OLang,
			ContentRating: r.ContentRating, Site: r.Site, ProductWorkID: r.ProductWorkID,
			UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		}
	}
	items, err := s.enrichWorkListItems(ctx, src, f.NSFW)
	if err != nil {
		return dto.PublicWorksListData{}, err
	}
	out := dto.PublicWorksListData{Items: items}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		c := publicCursor{Sort: lane, ID: last.ID}
		if lane == "updated" {
			c.Updated = last.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		nc := encodePublicCursor(c)
		out.NextCursor = &nc
	}
	return out, nil
}

// Changes serves the incremental works changes feed: LIVE galgame works
// ordered by (updated_at, id) ASC, resuming from the cursor. next_cursor is
// ALWAYS returned (it advances past the last row even on a short page, so a
// consumer keeps polling the same cursor for new rows). No nsfw gate. The feed
// deliberately trails real time by 5 seconds (see the watermark note below).
func (s *PublicService) Changes(ctx context.Context, cursor string, limit int) (dto.PublicChangesData, error) {
	cur, err := decodePublicCursor(cursor, "changes")
	if err != nil {
		return dto.PublicChangesData{}, err
	}
	// Defensive only — the handler is the wire authority (it 400s a bad limit).
	// Clamp at the ceiling rather than resetting to the default so both layers
	// agree on what an over-max limit means.
	if limit <= 0 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}
	// Watermark safety lag: updated_at is STATEMENT time, not commit time, so a
	// long transaction can commit a row whose updated_at already sits behind a
	// consumer's advanced cursor — that row would be skipped forever. Refusing
	// to serve rows younger than the lag means any transaction that commits
	// within 5s of its statement can no longer be missed. Changes feed ONLY:
	// the works-list `sort=updated` lane is a browse lane, not a sync watermark.
	where := []string{"deleted_at IS NULL", "status = ?", "medium_id = ?",
		"updated_at < now() - interval '5 seconds'"}
	args := []any{model.WorkStatusLive, galgameMediumID}
	if cur.Updated != "" {
		ts, perr := time.Parse(time.RFC3339Nano, cur.Updated)
		if perr != nil {
			return dto.PublicChangesData{}, ErrBadCursor
		}
		// Row-value comparison (not OR expansion) so the keyset lands as an
		// Index Cond on idx_catalog_work_updated_id instead of a Filter.
		where = append(where, "(updated_at, id) > (?, ?)")
		args = append(args, ts, cur.ID)
	}
	q := `SELECT id, updated_at FROM catalog_work WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY updated_at ASC, id ASC LIMIT ?`
	args = append(args, limit)
	var rows []struct {
		ID        int64
		UpdatedAt time.Time
	}
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicChangesData{}, err
	}
	out := dto.PublicChangesData{Items: make([]dto.PublicChangeItem, len(rows))}
	next := cur
	for i, r := range rows {
		out.Items[i] = dto.PublicChangeItem{
			EntityType: "work", ID: r.ID, Updated: r.UpdatedAt.UTC().Format(time.RFC3339),
		}
		next = publicCursor{Sort: "changes", ID: r.ID, Updated: r.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	}
	out.NextCursor = encodePublicCursor(next)
	return out, nil
}

// worksSortLane normalizes the works-list sort token to its cursor lane.
func worksSortLane(sort string) string {
	if sort == "updated" {
		return "updated"
	}
	return "id"
}

// ── W1-frozen enrichment helpers below (W2A calls, never edits) ──────────────

// workListSourceRow is what the WorksList page query produces per work — the
// raw registry columns enrichWorkListItems needs.
type workListSourceRow struct {
	ID            int64
	MediumID      int16
	DisplayName   string
	OLang         string
	ContentRating int16
	Site          *string
	ProductWorkID *int64
	UpdatedAt     string // RFC3339
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
