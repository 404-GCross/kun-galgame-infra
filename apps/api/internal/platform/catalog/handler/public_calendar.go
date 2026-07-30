// public_calendar.go — the release-calendar buckets on the frozen /v1/catalog
// public projection (A2-1c): the month view, the "month still unknown" year
// bucket, and the global TBA bucket.
//
// Two conventions worth spelling out because they differ from the taxonomy
// lanes:
//
//   - month / year default to the CURRENT Asia/Tokyo month / year. Galgame
//     release dates are Japanese civil dates, so "this month" is a JST
//     question; a fixed +09:00 zone (JST has no DST) keeps tzdata out of the
//     binary. The resolved value is echoed in the response.
//   - olang is an OPEN vocabulary (upstream BCP-47 tags), so an unrecognized
//     value yields an empty bucket rather than a 400 — the same posture as
//     `platform` on the works list, and the deliberate opposite of our own
//     closed vocabularies (content_rating / kind / tier), where a typo is a
//     caller mistake worth failing loudly.
package handler

import (
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// calendarJST pins the calendar's civil-date frame. Fixed offset: JST has no
// DST, so no tzdata dependency is needed to be correct.
var calendarJST = time.FixedZone("Asia/Tokyo", 9*60*60)

const (
	msgBadCalendarMonth = "month must be YYYY-MM"
	msgBadCalendarYear  = "year must be YYYY"
)

// Calendar serves GET /v1/catalog/calendar — one ISO month of works whose
// EARLIEST dated release falls in it (day and month precision alike), date ASC
// then id ASC, keyset-paged.
func (h *PublicHandler) Calendar(c fiber.Ctx) error {
	month := strings.TrimSpace(c.Query("month"))
	if month == "" {
		month = defaultCalendarMonth(time.Now())
	}
	t, err := time.Parse("2006-01", month)
	if err != nil || len(month) != 7 {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadCalendarMonth)
	}
	bucket := service.CalendarBucket{
		Kind: service.CalendarMonthBucket, Year: t.Year(), Month: int(t.Month()),
	}
	return h.serveCalendar(c, bucket, "month-"+month, func(d *dto.PublicCalendarData) { d.Month = month })
}

// CalendarPending serves GET /v1/catalog/calendar/pending — one year's works
// whose earliest release is known only to the year (id ASC, keyset-paged).
func (h *PublicHandler) CalendarPending(c fiber.Ctx) error {
	year := strings.TrimSpace(c.Query("year"))
	if year == "" {
		year = defaultCalendarYear(time.Now())
	}
	t, err := time.Parse("2006", year)
	if err != nil || len(year) != 4 {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadCalendarYear)
	}
	bucket := service.CalendarBucket{Kind: service.CalendarPendingBucket, Year: t.Year()}
	return h.serveCalendar(c, bucket, "pending-"+year, func(d *dto.PublicCalendarData) { d.Year = year })
}

// CalendarTBA serves GET /v1/catalog/calendar/tba — the global "announced, no
// date yet" bucket (id ASC, keyset-paged).
func (h *PublicHandler) CalendarTBA(c fiber.Ctx) error {
	return h.serveCalendar(c, service.CalendarBucket{Kind: service.CalendarTBABucket}, "tba", nil)
}

// serveCalendar runs the pipeline every bucket shares: parse the population
// gates → run the CHEAP meta query (count + max updated over the whole filtered
// set) → mint the ETag → short-circuit to 304 BEFORE any page is loaded or
// enriched → otherwise serve the keyset page.
func (h *PublicHandler) serveCalendar(c fiber.Ctx, b service.CalendarBucket, bucketKey string, echo func(*dto.PublicCalendarData)) error {
	f := service.CalendarFilter{
		NSFW:    nsfwQuery(c),
		OLang:   parsePublicOLang(c.Query("olang")),
		Include: service.ParseWorksListInclude(c.Query("include")),
	}
	// content_limit (A2-R5): a CLOSED vocabulary, so an unknown token is a 400 —
	// the deliberate opposite of olang above. It gates bucket membership, so it
	// also rides in the ETag's population key (CalendarFilter.PopulationKey).
	var ok bool
	if f.DisplayLimits, ok = displayLimitsPub(c.Query("content_limit")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadDisplayLimit)
	}
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}

	count, maxUpdated, err := h.svc.CalendarMeta(c.Context(), b, f)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	etag := service.CalendarETag(bucketKey, f.PopulationKey(), count, maxUpdated)
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheSearch)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}

	data, err := h.svc.CalendarPage(c.Context(), b, f, c.Query("cursor"), limit)
	if err != nil {
		if stderrors.Is(err, service.ErrBadCursor) {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadCursor)
		}
		return response.InternalError(c, errors.ErrInternalServer)
	}
	data.Count = count
	if data.Meta, err = h.calendarMeta(c, b, f); err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if echo != nil {
		echo(&data)
	}
	return response.Success(c, data)
}

// calendarMeta builds the bucket's navigation frame (A2-1e / R10).
//
// `today` is on every bucket; the month bounds and prev/next are on the MONTH
// bucket only — pending and tba are not month-addressed, so a prev/next arrow
// there would point at nothing. The bounds query runs under the caller's own
// population gates, so an sfw and an nsfw caller can legitimately see different
// ends of the calendar.
//
// It runs AFTER the 304 short-circuit on purpose: a fresh response is the only
// one that needs the frame, and a cache hit must stay a single cheap meta
// query. The frame is derived from the same population the ETag folds (nsfw ×
// olang) plus the requested month, both of which are already in the validator
// or in the URL — so it introduces no cache-correctness gap and does NOT enter
// the ETag key.
func (h *PublicHandler) calendarMeta(c fiber.Ctx, b service.CalendarBucket, f service.CalendarFilter) (dto.PublicCalendarMeta, error) {
	meta := dto.PublicCalendarMeta{Today: time.Now().In(calendarJST).Format("2006-01-02")}
	if b.Kind != service.CalendarMonthBucket {
		return meta, nil
	}
	minOrd, maxOrd, found, err := h.svc.CalendarBounds(c.Context(), f)
	if err != nil {
		return dto.PublicCalendarMeta{}, err
	}
	if !found {
		// Empty population: no bounds to publish, and nothing to page to in
		// either direction. has_prev / has_next are still stated (false) so a
		// UI can disable both arrows instead of guessing from absent keys.
		no := false
		meta.HasPrev, meta.HasNext = &no, &no
		return meta, nil
	}
	meta.MinMonth, meta.MaxMonth = monthOfOrdinal(minOrd), monthOfOrdinal(maxOrd)
	cur := int64(b.Year)*10000 + int64(b.Month)*100
	prev, next := cur > minOrd, cur < maxOrd
	meta.HasPrev, meta.HasNext = &prev, &next
	return meta, nil
}

// monthOfOrdinal renders a composed month ordinal (y*10000+m*100) as YYYY-MM.
func monthOfOrdinal(ord int64) string {
	return fmt.Sprintf("%04d-%02d", ord/10000, (ord/100)%100)
}

// defaultCalendarMonth / defaultCalendarYear resolve the omitted window in JST.
// Split out as pure functions of an instant so the timezone rule is testable
// without pinning a real "today".
func defaultCalendarMonth(now time.Time) string { return now.In(calendarJST).Format("2006-01") }
func defaultCalendarYear(now time.Time) string  { return now.In(calendarJST).Format("2006") }

// parsePublicOLang resolves ?olang= for the CALENDAR buckets: omitted → the
// default ja+zh family. The calendar is a CURATED surface — 新作月表 — and the
// family default is exactly what the wiki-era release calendar showed, so it
// stays. (The works product search resolves the same parameter against a
// different default; see worksSearchOLang.)
func parsePublicOLang(raw string) service.PublicOLang {
	return parsePublicOLangDefault(raw, service.PublicOLang{})
}

// worksSearchOLang resolves ?olang= for the works PRODUCT SEARCH lane: omitted →
// NO gate at all.
//
// The two lanes differ on purpose (144, the olang-restoration wave). Search and
// browse are the DISCOVERY surface: a caller who did not name a language is
// asking "what do you have", and answering only with ja+zh would — the moment
// the registry stopped being uniformly 'ja' — silently vanish every en/ru/ko
// work kungal, moyu and letmoe can find today. Curation belongs to the calendar;
// reachability belongs to search. Everything else about the parameter is
// identical across the lanes: same open vocabulary, same `all`, same explicit
// comma-separated set.
func worksSearchOLang(raw string) service.PublicOLang {
	return parsePublicOLangDefault(raw, service.PublicOLang{All: true})
}

// parsePublicOLangDefault is the one parser both lanes share; def is what an
// OMITTED olang resolves to. `all` → no gate, otherwise the comma-separated set
// (blank entries dropped; an all-blank list degrades to the caller's default
// rather than to an impossible IN ()).
func parsePublicOLangDefault(raw string, def service.PublicOLang) service.PublicOLang {
	switch trimmed := strings.TrimSpace(raw); trimmed {
	case "":
		return def
	case "all":
		return service.PublicOLang{All: true}
	default:
		var vals []string
		for _, p := range strings.Split(trimmed, ",") {
			if p = strings.TrimSpace(p); p != "" {
				vals = append(vals, p)
			}
		}
		if len(vals) == 0 {
			return def
		}
		return service.PublicOLang{Values: vals}
	}
}
