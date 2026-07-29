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
	if echo != nil {
		echo(&data)
	}
	return response.Success(c, data)
}

// defaultCalendarMonth / defaultCalendarYear resolve the omitted window in JST.
// Split out as pure functions of an instant so the timezone rule is testable
// without pinning a real "today".
func defaultCalendarMonth(now time.Time) string { return now.In(calendarJST).Format("2006-01") }
func defaultCalendarYear(now time.Time) string  { return now.In(calendarJST).Format("2006") }

// parsePublicOLang resolves ?olang=: omitted → the default ja+zh family,
// `all` → no gate, otherwise the comma-separated set (blank entries dropped; an
// all-blank list degrades to the default rather than to an impossible IN ()).
func parsePublicOLang(raw string) service.PublicOLang {
	switch trimmed := strings.TrimSpace(raw); trimmed {
	case "":
		return service.PublicOLang{}
	case "all":
		return service.PublicOLang{All: true}
	default:
		var vals []string
		for _, p := range strings.Split(trimmed, ",") {
			if p = strings.TrimSpace(p); p != "" {
				vals = append(vals, p)
			}
		}
		return service.PublicOLang{Values: vals}
	}
}
