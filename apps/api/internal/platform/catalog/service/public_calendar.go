// public_calendar.go — the release-calendar buckets on the frozen /v1/catalog
// public projection (A2-1c, refs/proj/126 D2 + P1).
//
// ── the classification anchor (轨长裁定, 2026-07-29) ──────────────────────────
//
// A work's bucket is decided by ONE number: the composed ordinal
// (y*10000 + m*100 + d, unknown month/day = 0) of its EARLIEST year-carrying,
// non-soft-deleted release. That is the SAME expression the works list projects
// as `release_date` and filters on with released_after/before, so a work's
// calendar bucket and the date printed on its list row can never disagree.
//
// The encoding buckets itself:
//
//	day precision   2024-06-14 → 20240614 ┐ both inside the June-2024 window
//	month precision 2024-06    → 20240600 ┘ [20240600, 20240699]
//	year precision  2024       → 20240000   outside every month window → pending
//	no dated release, but release rows exist → tba
//	no release rows at all                  → in NO bucket (unknown)
//
// ── query shape ──────────────────────────────────────────────────────────────
//
// The dated buckets could be written as the works list's correlated scalar
// subquery (`(SELECT min(...) ...) BETWEEN lo AND hi`), but that evaluates the
// aggregate once per candidate work — 245 ms for one month page against the
// 2026-07 dev registry. The shape below prunes to the works that have ANY
// release inside the window first, then takes each candidate's true earliest
// ordinal and requires IT to land in the window — literally the ruling above,
// and 38 ms for the same page. Verified row-for-row identical to the naive
// shape across all 144 month windows of 2015-2026 (39,834 works, 0 diffs).
//
// Zero migrations: no new index, no new column.
package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
)

// Cursor lanes of the three buckets. Each is its own lane token so a cursor can
// never be replayed across buckets (decodePublicCursor pins it).
const (
	calendarLaneMonth   = "calendar"
	calendarLanePending = "calendar-pending"
	calendarLaneTBA     = "calendar-tba"
)

// CalendarBucketKind selects which of the three buckets a request addresses.
type CalendarBucketKind int8

const (
	// CalendarMonthBucket is one ISO month of day- and month-precision works.
	CalendarMonthBucket CalendarBucketKind = iota
	// CalendarPendingBucket is one year's "month still unknown" works.
	CalendarPendingBucket
	// CalendarTBABucket is the global "announced, no date yet" bucket.
	CalendarTBABucket
)

// CalendarBucket addresses one bucket: the kind plus, for the dated buckets,
// the window it covers. Year/Month are plain civil numbers — the handler owns
// parsing and the Asia/Tokyo default.
type CalendarBucket struct {
	Kind  CalendarBucketKind
	Year  int // month + pending buckets
	Month int // month bucket only (1-12)
}

// window renders the bucket's inclusive composed-ordinal window. ok=false for
// the TBA bucket, which has no window at all (it is defined by the ABSENCE of a
// dated release).
func (b CalendarBucket) window() (lo, hi int64, ok bool) {
	switch b.Kind {
	case CalendarMonthBucket:
		lo = int64(b.Year)*10000 + int64(b.Month)*100
		// +99 catches every legal day (1-31) AND the month-precision d=0 row.
		return lo, lo + 99, true
	case CalendarPendingBucket:
		// Year precision is the single ordinal y*10000 — the smallest value the
		// year can produce, which is exactly why a work only lands here when its
		// earliest release carries no month.
		lo = int64(b.Year) * 10000
		return lo, lo, true
	default:
		return 0, 0, false
	}
}

// lane is the bucket's cursor lane token.
func (b CalendarBucket) lane() string {
	switch b.Kind {
	case CalendarMonthBucket:
		return calendarLaneMonth
	case CalendarPendingBucket:
		return calendarLanePending
	default:
		return calendarLaneTBA
	}
}

// PublicOLang is the original-language population gate (P1), shared by the
// calendar buckets and the works product search — one olang means one thing
// across the whole public face. The zero value is the DEFAULT family (ja +
// every zh variant), the JP/CN audience the 新作月表 exists for. olang is an
// OPEN vocabulary (upstream BCP-47 tags), so an unrecognized value yields an
// empty result, never a 400.
type PublicOLang struct {
	// All turns the gate off entirely (olang=all).
	All bool
	// Values is an explicit olang set; empty (with All false) = the default family.
	Values []string
}

// predicate renders the gate's SQL over the catalog_work alias w ("" = no gate).
func (o PublicOLang) predicate() (string, []any) {
	switch {
	case o.All:
		return "", nil
	case len(o.Values) > 0:
		return "w.olang IN ?", []any{o.Values}
	default:
		// Grounded 2026-07-29 against the registry: olang holds the upstream
		// BCP-47 spelling (ja / zh-Hans / zh-Hant / en / ko / ru …), never the
		// product locale form (ja-jp / zh-cn) the deprecated wiki face used. The
		// prefix match therefore covers both Chinese scripts and any future
		// zh-* variant without a vocabulary list to maintain.
		return "(w.olang = ? OR w.olang LIKE ?)", []any{"ja", "zh%"}
	}
}

// meiliFilter renders the SAME gate as a Meilisearch filter expression ("" = no
// gate). It lives next to predicate() on purpose: the two encodings of one
// population rule must be read together, and `STARTS WITH 'zh'` is the exact
// counterpart of SQL's `LIKE 'zh%'` (a stock operator since Meilisearch 1.8 —
// unlike CONTAINS it needs no experimental flag; verified against the pinned
// v1.45 image).
func (o PublicOLang) meiliFilter() string {
	switch {
	case o.All:
		return ""
	case len(o.Values) > 0:
		quoted := make([]string, len(o.Values))
		for i, v := range o.Values {
			quoted[i] = "'" + catsearch.EscapeFilterValue(v) + "'"
		}
		return "olang IN [" + strings.Join(quoted, ", ") + "]"
	default:
		return "(olang = 'ja' OR olang STARTS WITH 'zh')"
	}
}

// Key is the ETag discriminator of this gate — two different populations must
// never share a cache validator.
func (o PublicOLang) Key() string {
	switch {
	case o.All:
		return "all"
	case len(o.Values) > 0:
		return strings.Join(o.Values, "+")
	default:
		return "jazh"
	}
}

// CalendarFilter is the shared request shape of all three buckets: the
// population gates plus the include= projection selector.
type CalendarFilter struct {
	NSFW    bool
	OLang   PublicOLang
	Include WorksListInclude
}

// PopulationKey is the ETag discriminator of the whole population (nsfw × olang).
func (f CalendarFilter) PopulationKey() string {
	gate := "sfw"
	if f.NSFW {
		gate = "nsfw"
	}
	return gate + "-" + f.OLang.Key()
}

// CalendarMeta returns the bucket's (row count, newest updated_at) over the
// WHOLE filtered set — the cheap ETag basis, so a matching If-None-Match can
// 304 without ever loading (and enriching) a page.
//
// max(updated_at) is the right freshness signal even though release rows carry
// no updated_at of their own: every facet write touches its host work
// (repository.TouchWorks — the same discipline the changes feed rides on), so a
// release date edit moves the work's stamp.
func (s *PublicService) CalendarMeta(ctx context.Context, b CalendarBucket, f CalendarFilter) (int64, time.Time, error) {
	from, where, args := calendarSource(b, f)
	var row struct {
		N          int64     `gorm:"column:n"`
		MaxUpdated time.Time `gorm:"column:max_updated"`
	}
	q := `SELECT count(*) AS n, coalesce(max(w.updated_at), to_timestamp(0)) AS max_updated ` +
		from + ` WHERE ` + strings.Join(where, " AND ")
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&row).Error; err != nil {
		return 0, time.Time{}, err
	}
	return row.N, row.MaxUpdated, nil
}

// CalendarPage serves one keyset page of a bucket. Items are the works-list
// items verbatim (PublicWorkListItem), include= and all — the calendar adds no
// item field of its own, so a consumer renders a calendar row with exactly the
// code that renders a browse row. A malformed / cross-bucket cursor is
// ErrBadCursor (the handler maps it to 400).
func (s *PublicService) CalendarPage(ctx context.Context, b CalendarBucket, f CalendarFilter, cursor string, limit int) (dto.PublicCalendarData, error) {
	cur, err := decodePublicCursor(cursor, b.lane())
	if err != nil {
		return dto.PublicCalendarData{}, err
	}
	limit = clampBrowseLimit(limit)

	from, where, args := calendarSource(b, f)
	sel := `SELECT w.id, w.medium_id, w.display_name, w.olang, w.content_rating, w.site, w.product_work_id, w.claim_state, w.updated_at`
	order := ` ORDER BY w.id ASC`
	if b.Kind == CalendarMonthBucket {
		// The month bucket is the only one that sorts by date: pending is a
		// single ordinal and tba has none, so both fall back to id ASC.
		sel += `, e.ord`
		order = ` ORDER BY e.ord ASC, w.id ASC`
		if cur.ID > 0 {
			// Row-value comparison, not OR expansion — the same keyset form the
			// works list uses on (updated_at, id).
			where = append(where, "(e.ord, w.id) > (?, ?)")
			args = append(args, cur.Ord, cur.ID)
		}
	} else if cur.ID > 0 {
		where = append(where, "w.id > ?")
		args = append(args, cur.ID)
	}
	args = append(args, limit)

	var rows []struct {
		ID          int64
		MediumID    int16
		DisplayName string
		// Explicit column tag: GORM snake-cases the field to o_lang, which
		// matches no result column (the W1 works-list trap, fixed in 0dce1f36).
		OLang         string `gorm:"column:olang"`
		ContentRating int16
		Site          *string
		ProductWorkID *int64
		ClaimState    *int16 `gorm:"column:claim_state"`
		UpdatedAt     time.Time
		Ord           int64 `gorm:"column:ord"`
	}
	q := sel + " " + from + ` WHERE ` + strings.Join(where, " AND ") + order + ` LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return dto.PublicCalendarData{}, err
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
		return dto.PublicCalendarData{}, err
	}
	out := dto.PublicCalendarData{Items: items}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		nc := encodePublicCursor(publicCursor{Sort: b.lane(), ID: last.ID, Ord: last.Ord})
		out.NextCursor = &nc
	}
	return out, nil
}

// calendarSource renders the FROM clause and the population predicates shared
// by the meta and page queries of one bucket. FIXED internal SQL — the only
// caller input reaching it is bound as arguments.
func calendarSource(b CalendarBucket, f CalendarFilter) (from string, where []string, args []any) {
	from = `FROM catalog_work w`
	if lo, hi, dated := b.window(); dated {
		// Candidate works = anything with a release inside the window; of those,
		// keep the ones whose TRUE earliest dated release also lands inside it.
		from += `
			JOIN (SELECT r.work_id, min(` + releaseOrd("r") + `) AS ord
				FROM catalog_release r
				WHERE r.released_y IS NOT NULL AND r.deleted_at IS NULL
				  AND r.work_id IN (SELECT c.work_id FROM catalog_release c
					WHERE c.released_y IS NOT NULL AND c.deleted_at IS NULL
					  AND ` + releaseOrd("c") + ` BETWEEN ? AND ?)
				GROUP BY r.work_id
				HAVING min(` + releaseOrd("r") + `) BETWEEN ? AND ?) e ON e.work_id = w.id`
		args = append(args, lo, hi, lo, hi)
	}

	// The works-list population predicate, verbatim.
	where = []string{"w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?"}
	args = append(args, model.WorkStatusLive, galgameMediumID)
	if !f.NSFW {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	if pred, pargs := f.OLang.predicate(); pred != "" {
		where = append(where, pred)
		args = append(args, pargs...)
	}
	if b.Kind == CalendarTBABucket {
		// Announced but undated: release rows exist, none of them carries a
		// year. A work with NO release row at all is "unknown" and deliberately
		// enters no bucket.
		where = append(where,
			"EXISTS (SELECT 1 FROM catalog_release r WHERE r.work_id = w.id AND r.deleted_at IS NULL)",
			"NOT EXISTS (SELECT 1 FROM catalog_release r WHERE r.work_id = w.id AND r.deleted_at IS NULL AND r.released_y IS NOT NULL)")
	}
	return from, where, args
}

// releaseOrd renders the composed date ordinal over a catalog_release alias —
// the single expression every calendar predicate and the works list's
// release_date projection share.
func releaseOrd(alias string) string {
	return alias + ".released_y::int * 10000 + coalesce(" + alias + ".released_m,0)::int * 100 + coalesce(" + alias + ".released_d,0)::int"
}

// CalendarBounds returns the earliest and latest month (composed ordinals
// y*10000+m*100) that hold at least one member under THIS caller's population
// gates — the navigation frame's min_month / max_month (A2-1e / R10).
// found=false when the whole population is empty (there is then no month to
// navigate to, and the meta omits the bounds rather than inventing them).
//
// Same-gate guarantee: the predicate is calendarSource's for a month-kind
// bucket MINUS the window join, so "the newest month with anything in it"
// is scoped by exactly the nsfw × olang gates that decide bucket membership.
// A work is placed by its EARLIEST dated release — the same anchor as
// release_date and as the buckets themselves — so a bound month is always a
// month whose bucket is genuinely non-empty.
//
// Cost: one aggregate over the release table's dated rows joined to the gated
// work population. It rides the same request as the ETag meta query, i.e. it
// is paid once per calendar call, not per page.
func (s *PublicService) CalendarBounds(ctx context.Context, f CalendarFilter) (minOrd, maxOrd int64, found bool, err error) {
	where := []string{"w.deleted_at IS NULL", "w.status = ?", "w.medium_id = ?"}
	args := []any{model.WorkStatusLive, galgameMediumID}
	if !f.NSFW {
		where = append(where, "w.content_rating <> ?")
		args = append(args, model.ContentRatingR18)
	}
	if pred, pargs := f.OLang.predicate(); pred != "" {
		where = append(where, pred)
		args = append(args, pargs...)
	}
	var row struct {
		MinOrd *int64 `gorm:"column:min_ord"`
		MaxOrd *int64 `gorm:"column:max_ord"`
	}
	// The inner aggregate is each work's earliest dated release ordinal; the
	// outer one takes the extremes over the GATED population. Year-precision
	// works (d=m=0) are deliberately included: their ordinal floors to the
	// year's January, which is the correct lower bound for month navigation.
	q := `SELECT min(e.ord) AS min_ord, max(e.ord) AS max_ord
		FROM (SELECT r.work_id, min(` + releaseOrd("r") + `) AS ord
			FROM catalog_release r
			WHERE r.released_y IS NOT NULL AND r.deleted_at IS NULL
			GROUP BY r.work_id) e
		JOIN catalog_work w ON w.id = e.work_id
		WHERE ` + strings.Join(where, " AND ")
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&row).Error; err != nil {
		return 0, 0, false, err
	}
	if row.MinOrd == nil || row.MaxOrd == nil {
		return 0, 0, false, nil
	}
	// Floor both to their month: a bound is a MONTH, and a day-precision
	// extreme must not make has_next look false for the rest of its month.
	return (*row.MinOrd / 100) * 100, (*row.MaxOrd / 100) * 100, true, nil
}

// CalendarETag mints a bucket's weak validator from the cheap meta pair. The
// population gates ride in the key: an sfw and an nsfw caller are looking at
// two different sets, and their counts may coincide by accident.
func CalendarETag(bucketKey, populationKey string, count int64, maxUpdated time.Time) string {
	return `W/"cal-` + bucketKey + `-` + populationKey + `-` +
		strconv.FormatInt(count, 10) + `-` + strconv.FormatInt(maxUpdated.Unix(), 10) + `"`
}
