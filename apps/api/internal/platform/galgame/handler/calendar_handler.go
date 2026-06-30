package handler

import (
	"fmt"
	"log/slog"
	"time"

	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// Release-calendar HTTP handlers. See docs/galgame_wiki/06-release-calendar-design.md.

// jstZone: galgame release dates are Japanese civil dates, so "current month"
// and "today" are computed in JST. A fixed offset (JST has no DST) avoids a
// tzdata dependency in the binary.
var jstZone = time.FixedZone("Asia/Tokyo", 9*60*60)

// setCalendarCache writes the ETag + Cache-Control and reports whether the
// request's If-None-Match already matches (caller should then 304). Past months
// get a longer shared-cache window than the mutable current/future months;
// max-age=0 keeps the browser revalidating (cheap 304 via ETag) so an edit is
// never served stale to a client. Per-month CDN purge is wired in P5.
func setCalendarCache(c fiber.Ctx, etag, cacheTag string, isPast bool) bool {
	c.Set("ETag", etag)
	// Per-month/bucket cache tag so a CDN (Cloudflare) can purge exactly this key
	// on edit. Until a CDN purge is wired, the ETag (which embeds max(updated))
	// already busts caches on edit within the s-maxage window — purge only makes
	// it immediate. See docs/galgame_wiki/06-release-calendar-design.md §8.
	c.Set("Cache-Tag", cacheTag)
	if isPast {
		c.Set("Cache-Control", "public, max-age=0, s-maxage=86400, stale-while-revalidate=3600")
	} else {
		c.Set("Cache-Control", "public, max-age=0, s-maxage=300, stale-while-revalidate=60")
	}
	return c.Get("If-None-Match") == etag
}

// Calendar serves one ISO month (?month=YYYY-MM, default = current JST month) of
// releases at day + month precision — published + unclaimed VNDB drafts (each
// item's `status` distinguishes them), released and upcoming mixed, ascending by
// date. Year-only and TBA titles live in /calendar/pending and /calendar/tba.
func (h *GalgameHandler) Calendar(c fiber.Ctx) error {
	monthStr := c.Query("month")
	if monthStr == "" {
		monthStr = time.Now().In(jstZone).Format("2006-01")
	}
	mt, err := time.ParseInLocation("2006-01", monthStr, jstZone)
	if err != nil || len(monthStr) != 7 {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "invalid month (want YYYY-MM)")
	}
	start := time.Date(mt.Year(), mt.Month(), 1, 0, 0, 0, 0, jstZone)
	startStr := start.Format("2006-01-02")
	nextStr := start.AddDate(0, 1, 0).Format("2006-01-02")
	cl := utils.ParseContentLimit(c.Query("content_limit"), "sfw")

	count, maxUpdated, err := h.galgameService.CalendarMonthMeta(c.Context(), startStr, nextStr, cl)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	etag := fmt.Sprintf(`W/"cal-%s-%s-%d-%d"`, monthStr, cl, count, maxUpdated.Unix())

	now := time.Now().In(jstZone)
	curStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, jstZone)
	if setCalendarCache(c, etag, "gal-cal-"+monthStr, start.Before(curStart)) {
		return c.SendStatus(fiber.StatusNotModified)
	}

	items, err := h.galgameService.CalendarMonth(c.Context(), startStr, nextStr, cl)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	// Navigable range so a client can disable prev/next at the data edges. A
	// bounds error degrades to has_prev/has_next=false (nav disabled) — advisory
	// only, never fails the request (items already rendered) — but log it so a
	// broken query stays observable. NOTE: bounds aren't folded into the ETag, so
	// on a 304 has_next can lag a newly-added far-future month until s-maxage
	// lapses — self-correcting (the phantom next month is an empty first-class
	// state), traded for the cheap-304 win.
	minMonth, maxMonth, err := h.galgameService.CalendarBounds(c.Context(), cl)
	if err != nil {
		slog.Warn("galgame calendar: bounds query failed", "err", err)
	}
	prev := start.AddDate(0, -1, 0).Format("2006-01")
	next := start.AddDate(0, 1, 0).Format("2006-01")
	return response.Success(c, fiber.Map{
		"month": monthStr,
		"today": now.Format("2006-01-02"),
		"items": items,
		"links": fiber.Map{
			"self": "/api/galgame/calendar?month=" + monthStr,
			"prev": "/api/galgame/calendar?month=" + prev,
			"next": "/api/galgame/calendar?month=" + next,
		},
		"meta": fiber.Map{
			"prev_month": prev,
			"next_month": next,
			"has_prev":   minMonth != "" && monthStr > minMonth,
			"has_next":   maxMonth != "" && monthStr < maxMonth,
			"min_month":  minMonth,
			"max_month":  maxMonth,
			"count":      count,
		},
	})
}

// CalendarPending serves the "month TBD" bucket for a year (?year=YYYY,
// default = current JST year): games whose release is known only to the year.
func (h *GalgameHandler) CalendarPending(c fiber.Ctx) error {
	yearStr := c.Query("year")
	if yearStr == "" {
		yearStr = time.Now().In(jstZone).Format("2006")
	}
	yt, err := time.ParseInLocation("2006", yearStr, jstZone)
	if err != nil || len(yearStr) != 4 {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "invalid year (want YYYY)")
	}
	yStart := time.Date(yt.Year(), 1, 1, 0, 0, 0, 0, jstZone)
	yStartStr := yStart.Format("2006-01-02")
	yNextStr := yStart.AddDate(1, 0, 0).Format("2006-01-02")
	cl := utils.ParseContentLimit(c.Query("content_limit"), "sfw")

	count, maxUpdated, err := h.galgameService.CalendarYearPendingMeta(c.Context(), yStartStr, yNextStr, cl)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	etag := fmt.Sprintf(`W/"calpend-%s-%s-%d-%d"`, yearStr, cl, count, maxUpdated.Unix())
	if setCalendarCache(c, etag, "gal-cal-pending-"+yearStr, false) { // tracks live edits → short cache
		return c.SendStatus(fiber.StatusNotModified)
	}

	items, err := h.galgameService.CalendarYearPending(c.Context(), yStartStr, yNextStr, cl)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{
		"year":  yearStr,
		"items": items,
		"meta":  fiber.Map{"count": count},
	})
}

// CalendarTBA serves the global "release date pending" (TBA) bucket.
func (h *GalgameHandler) CalendarTBA(c fiber.Ctx) error {
	cl := utils.ParseContentLimit(c.Query("content_limit"), "sfw")

	count, maxUpdated, err := h.galgameService.CalendarTBAMeta(c.Context(), cl)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	etag := fmt.Sprintf(`W/"caltba-%s-%d-%d"`, cl, count, maxUpdated.Unix())
	if setCalendarCache(c, etag, "gal-cal-tba", false) {
		return c.SendStatus(fiber.StatusNotModified)
	}

	items, err := h.galgameService.CalendarTBA(c.Context(), cl)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{
		"items": items,
		"meta":  fiber.Map{"count": count},
	})
}
