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

var calendarJST = time.FixedZone("Asia/Tokyo", 9*60*60)

const (
	msgBadCalendarMonth = "month must be YYYY-MM"
	msgBadCalendarYear  = "year must be YYYY"
)

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

func (h *PublicHandler) CalendarTBA(c fiber.Ctx) error {
	return h.serveCalendar(c, service.CalendarBucket{Kind: service.CalendarTBABucket}, "tba", nil)
}

func (h *PublicHandler) serveCalendar(c fiber.Ctx, b service.CalendarBucket, bucketKey string, echo func(*dto.PublicCalendarData)) error {
	f := service.CalendarFilter{
		NSFW:    nsfwQuery(c),
		OLang:   parsePublicOLang(c.Query("olang")),
		Include: service.ParseWorksListInclude(c.Query("include")),
	}
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

func monthOfOrdinal(ord int64) string {
	return fmt.Sprintf("%04d-%02d", ord/10000, (ord/100)%100)
}

func defaultCalendarMonth(now time.Time) string { return now.In(calendarJST).Format("2006-01") }
func defaultCalendarYear(now time.Time) string  { return now.In(calendarJST).Format("2006") }

func parsePublicOLang(raw string) service.PublicOLang {
	return parsePublicOLangDefault(raw, service.PublicOLang{})
}

func worksSearchOLang(raw string) service.PublicOLang {
	return parsePublicOLangDefault(raw, service.PublicOLang{All: true})
}

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
