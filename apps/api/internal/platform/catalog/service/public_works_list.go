// public_works_list.go — the works browse lane (GET /v1/catalog/works) and
// the changes feed (GET /v1/catalog/changes), doc 106 G1/G2.
//
// Population invariant (both lanes): the public fetchable set — galgame
// medium, status=live, not soft-deleted — exactly the wave-105 works-index
// population. Wave 186a made the works LIST's status axis a FILTER
// (WorksListFilter.Statuses) whose empty value is still {live}, so the
// invariant holds for every caller who does not pass the moderator-gated
// status= parameter; the CHANGES feed keeps the literal, because a public sync
// watermark that could be widened per caller would hand different consumers
// different id sets under one cursor. The works LIST additionally drops r18 rows unless nsfw; the
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

// WorksListFilter is the works browse lane's request shape: the filter set
// (all optional; zero value = the whole fetchable set) plus the include=
// projection selector. Date bounds are composed ordinals
// (y*10000 + m*100 + d) over the EARLIEST release date per work.
type WorksListFilter struct {
	ContentRating *int16 // model.ContentRating* — nil = all (r18 still needs NSFW)
	Claimed       *bool  // true = claimed only; false = bodyless only; nil = both
	// ClaimStates narrows to a set of PUBLIC claim states (none|live|draft|
	// hidden, A2-R4) — the values claimed_by.state renders on the very items
	// this lane returns. Empty = no gate at all, so every pre-existing caller's
	// wire stays byte-identical.
	//
	// It is a different axis from Claimed, not a finer spelling of it: `claimed`
	// answers "does a product own this row", this answers "may it be shown". An
	// entity page listing its member works passes claim_state=live — without it
	// there is no server-side way to keep DRAFT (unpublished) stubs and
	// unclaimed rows off a public list, which is exactly how they reached
	// production. Semantics are the works/search parameter's, word for word.
	ClaimStates []string
	// DisplayLimits narrows to a set of EDITORIAL DISPLAY limits (sfw|nsfw,
	// A2-R5) — the values claimed_by.content_limit renders on these very items.
	// Empty = no gate at all, so every pre-existing caller's wire stays
	// byte-identical.
	//
	// It is a different axis from ContentRating, not a finer spelling of it:
	// content_rating answers "what is the GAME rated", this answers "is the
	// material we would RENDER safe to publish". A site building its indexable
	// surface passes content_limit=sfw — mapping the age axis onto that question
	// instead is what collapsed one downstream's SEO surface from 6,117 works to
	// 599 (doc 106 §38).
	DisplayLimits []string
	// Site narrows to the works claimed by ONE tenant (catalog_work.site) —
	// the sibling of the parameter PendingClaims and the S2S work search
	// already take (wave 161 P5, 162 §4 ruling ①). Empty = no gate at all, so
	// every pre-existing caller's wire stays byte-identical.
	//
	// A product's own review queue and "my site's works" lane cannot be built
	// without it: filtering another tenant's rows out AFTER the page is fetched
	// makes both the page size and the keyset cursor lie. This is a LIVE SQL
	// predicate inside the LIMIT, so a claim that moves tenant is reflected on
	// the very next call — the works search index carries no site facet and
	// deliberately gains none.
	Site string
	// Statuses narrows the REGISTRY status axis (catalog_work.status, i.e.
	// model.WorkStatus*). EMPTY IS NOT "no gate" here — it is {live}, the
	// population every pre-existing caller of this lane has ever seen, so the
	// default page stays byte-identical while the axis stops being a literal
	// buried in the WHERE.
	//
	// It is the third axis beside ClaimStates and DisplayLimits and answers a
	// different question from both: `status` is what the REGISTRY thinks of the
	// row (live / stub — below the metadata bar / merged — a tombstone),
	// `claim_state` is what the owning PRODUCT thinks of its claim. Only the
	// moderator queue view (wave 186a) ever widens it, and never to merged:
	// status=2 answering anything but 404 is a contract break.
	Statuses []int16
	LabelID  int64 // via catalog_work_label
	// LabelRollup widens LabelID to the label's imprints and subsidiaries
	// (wave 199). Ignored without LabelID. Every row that came in through a
	// child carries via_label — see public_label_rollup.go for why the
	// attribution is not optional.
	LabelRollup bool
	// TagIDs are canonical tag ids ANDed together (A2-1e): a work must carry a
	// source tag mapped to EVERY id, which is what a facet sidebar's "narrow by
	// another tag" means. One id behaves exactly as the pre-A2-1e scalar did.
	TagIDs         []int64 // via catalog_tag_source_map ⋈ catalog_work_tag
	SeriesID       int64   // via catalog_series_member
	EngineID       int64   // via catalog_work_engine (A2-1b)
	Platform       string  // release-level platform ∪ work-level platform rows
	ReleasedAfter  int64   // composed ordinal, inclusive; 0 = unbounded
	ReleasedBefore int64   // composed ordinal, inclusive; 0 = unbounded
	IDs            []int64
	NSFW           bool
	Sort           string // "id" (default, ASC) | "updated" (DESC, newest first)
	// Include selects the optional rich-brief blocks (A2-1a). The zero value
	// asks for none, which is what keeps the default page byte-identical to
	// the frozen W1 contract.
	Include WorksListInclude
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

	// The registry status axis. An absent filter is the frozen public default
	// {live}, so this clause is the same single-value predicate it has always
	// been unless a caller was explicitly authorized to widen it.
	statuses := f.Statuses
	if len(statuses) == 0 {
		statuses = []int16{model.WorkStatusLive}
	}
	where := []string{"w.deleted_at IS NULL", "w.status IN ?", "w.medium_id = ?"}
	args := []any{statuses, galgameMediumID}

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
	if f.Site != "" {
		// ANDed into the SAME conjunction as every other filter — i.e. inside
		// the LIMIT — so the page the caller receives is a page of THIS tenant's
		// works and the next_cursor it derives is honest.
		where = append(where, "w.site = ?")
		args = append(args, f.Site)
	}
	if pred, pargs := claimStateWhere(f.ClaimStates); pred != "" {
		// ONE clause in the SAME conjunction as every other filter. This face
		// emits no total (keyset paging: items + next_cursor), so "the count and
		// the rows share one gate" holds by construction — there is exactly one
		// query, and adding a count later would inherit this WHERE with it.
		where = append(where, pred)
		args = append(args, pargs...)
	}
	if pred, pargs := displayLimitWhere(f.DisplayLimits); pred != "" {
		// The editorial display axis (A2-R5), ANDed into the SAME conjunction as
		// the nsfw age gate and the claim-state gate — three orthogonal doors, one
		// query, so a caller opening one never widens another.
		where = append(where, pred)
		args = append(args, pargs...)
	}
	if f.LabelID > 0 {
		if f.LabelRollup {
			// The company page's population: this label's own works UNION the
			// works of its one-hop imprints/subsidiaries. One EXISTS, so the
			// roll-up is a widened probe inside the same conjunction — the
			// keyset page and its next_cursor stay honest about the set they
			// describe. Each row's attribution is restored below.
			where = append(where, `EXISTS (SELECT 1 FROM catalog_work_label wl
				WHERE wl.work_id = w.id AND (wl.label_id = ? OR wl.label_id IN (`+labelRollupChildren+`)))`)
			args = append(args, f.LabelID, f.LabelID, labelRollupRelations)
		} else {
			where = append(where, "EXISTS (SELECT 1 FROM catalog_work_label wl WHERE wl.work_id = w.id AND wl.label_id = ?)")
			args = append(args, f.LabelID)
		}
	}
	for _, tagID := range f.TagIDs {
		// Canonical tag → any source tag mapped to it (idx on catalog_work_tag
		// (work_id,...) unique carries the correlated work_id probe). ONE EXISTS
		// per requested tag — conjunctive by construction, and each probe stays
		// index-served (a single IN + HAVING count would force an aggregate).
		where = append(where, `EXISTS (SELECT 1 FROM catalog_work_tag wt
			JOIN catalog_tag_source_map m ON m.source_id = wt.source_id AND m.source_name = wt.name
			WHERE wt.work_id = w.id AND m.tag_id = ?)`)
		args = append(args, tagID)
	}
	if f.SeriesID > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM catalog_series_member sm WHERE sm.work_id = w.id AND sm.series_id = ?)")
		args = append(args, f.SeriesID)
	}
	if f.EngineID > 0 {
		// catalog_work_engine.engine_id carries its own reverse index, so the
		// correlated probe is an index lookup (A2-1b).
		where = append(where, "EXISTS (SELECT 1 FROM catalog_work_engine we WHERE we.work_id = w.id AND we.engine_id = ?)")
		args = append(args, f.EngineID)
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

	q := `SELECT w.id, w.medium_id, w.display_name, w.olang, w.content_rating, w.site, w.product_work_id, w.claim_state, w.updated_at
		FROM catalog_work w WHERE ` + strings.Join(where, " AND ") + " " + order + " LIMIT ?"
	args = append(args, limit)

	var rows []struct {
		ID          int64
		MediumID    int16
		DisplayName string
		// The explicit column tag is load-bearing: GORM snake-cases the field
		// to o_lang, which matches no result column, so the value silently
		// scanned as "" from W1 until A2-1a caught it.
		OLang         string `gorm:"column:olang"`
		ContentRating int16
		Site          *string
		ProductWorkID *int64
		ClaimState    *int16 `gorm:"column:claim_state"`
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
			ClaimState: r.ClaimState, UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339),
		}
	}
	items, err := s.enrichWorkListItems(ctx, src, f.NSFW, f.Include)
	if err != nil {
		return dto.PublicWorksListData{}, err
	}
	if f.LabelID > 0 && f.LabelRollup {
		ids := make([]int64, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		via, verr := s.labelRollupVia(ctx, f.LabelID, ids)
		if verr != nil {
			return dto.PublicWorksListData{}, verr
		}
		for i := range items {
			if v, ok := via[items[i].ID]; ok {
				items[i].ViaLabel = &v
			}
		}
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
	ClaimState    *int16
	UpdatedAt     string // RFC3339
}

// enrichWorkListItems projects one page of registry rows to the public list
// items, batch-attaching release_date (earliest release, partial ISO) and one
// representative cover (portrait pin first; sexual-flagged covers are never
// served to sfw callers), then whatever include= asked for on top. Order is
// preserved. The cover set is loaded ONCE and shared with the covers block.
func (s *PublicService) enrichWorkListItems(ctx context.Context, rows []workListSourceRow, nsfw bool, inc WorksListInclude) ([]dto.PublicWorkListItem, error) {
	if len(rows) == 0 {
		return []dto.PublicWorkListItem{}, nil
	}
	ids := make([]int64, len(rows))
	subjects := make([]claimSubject, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
		subjects[i] = claimSubject{WorkID: r.ID}
	}
	dates, err := s.earliestReleaseDatesFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	covers, err := s.read.loadWorkCovers(ctx, subjects)
	if err != nil {
		return nil, err
	}
	// The display axis (A2-R5), one batched wiki-body read per page — the same
	// shape (and the same claim partition) the cover bridge above rides on. This
	// is the ONE place the works list, the works search and the three calendar
	// buckets all fill claimed_by.content_limit from: they share this function.
	limits, err := s.read.loadDisplayNSFW(ctx, subjects)
	if err != nil {
		return nil, err
	}
	out := make([]dto.PublicWorkListItem, len(rows))
	for i, r := range rows {
		out[i] = dto.PublicWorkListItem{
			ID: r.ID, Medium: s.mediumKey(r.MediumID), DisplayName: r.DisplayName,
			ContentRating: contentRatingKey(r.ContentRating), OLang: r.OLang,
			ReleaseDate: dates[r.ID],
			ClaimedBy:   claimedBy(r.Site, r.ProductWorkID, r.ClaimState, limits[r.ID], r.ContentRating),
			Cover:       s.pickListCover(covers[r.ID], nsfw && limits[r.ID]), Updated: r.UpdatedAt,
		}
	}
	if err := s.attachWorkListBlocks(ctx, out, rows, subjects, covers, inc, nsfw, limits); err != nil {
		return nil, err
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
// pin first, then the loader's (sort_order, image_hash) order. allowSexual
// carries the same conjunction the slot picker runs on — the caller's age gate
// AND the work's editorial display flag — so a work that declares its art
// display-safe never represents itself with a sexual-flagged cover ("" when
// none qualifies).
func (s *PublicService) pickListCover(rows []WorkCoverRow, allowSexual bool) string {
	var fallback string
	for _, c := range rows {
		if !allowSexual && c.Sexual != 0 {
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
