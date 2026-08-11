package handler

import (
	stderrors "errors"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/news/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

const (
	msgBadLimit  = "limit must be an integer in [1,50]"
	msgBadCursor = "cursor is malformed or belongs to a different sort"
	msgBadTime   = "published_after and published_before must be RFC3339"
	msgBadWorkID = "work_id must be a positive integer"
	msgOffline   = "news feed is unavailable"

	cacheFeed = "public, max-age=60"
)

type PublicHandler struct {
	svc *service.PublicService
}

// NewPublicHandler tolerates a nil service: kun_news is a second database on a
// process whose primary job is catalog, and an unreachable news database must
// degrade the news face to 503 rather than take catalog down with it. The first
// production deploy runs before `CREATE DATABASE kun_news` on purpose-built
// order, and a hard failure there would crash-loop the whole catalog container.
func NewPublicHandler(svc *service.PublicService) *PublicHandler {
	return &PublicHandler{svc: svc}
}

func (h *PublicHandler) offline(c fiber.Ctx) bool {
	if h.svc != nil {
		return false
	}
	_ = response.Error(c, fiber.StatusServiceUnavailable, errors.ErrInternalServer, msgOffline)
	return true
}

func (h *PublicHandler) List(c fiber.Ctx) error {
	if h.offline(c) {
		return nil
	}
	f := service.FeedFilter{Sources: parseCSV(c.Query("source"))}

	var ok bool
	if f.PublishedAfter, ok = parseTime(c.Query("published_after")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadTime)
	}
	if f.PublishedBefore, ok = parseTime(c.Query("published_before")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadTime)
	}
	if f.WorkID, ok = parseWorkID(c.Query("work_id")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadWorkID)
	}
	limit, ok := parseLimit(c.Query("limit"))
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}

	count, maxPublished, maxID, err := h.svc.FeedMeta(c.Context(), f)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	etag := service.FeedETag(f.PopulationKey(), count, maxPublished, maxID)
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheFeed)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}

	data, err := h.svc.Feed(c.Context(), f, c.Query("cursor"), limit)
	if err != nil {
		if stderrors.Is(err, service.ErrBadCursor) {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadCursor)
		}
		return response.InternalError(c, errors.ErrInternalServer)
	}
	data.Count = count
	return response.Success(c, data)
}

func (h *PublicHandler) Detail(c fiber.Ctx) error {
	if h.offline(c) {
		return nil
	}
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return response.NotFound(c, errors.ErrNotFound)
	}
	item, err := h.svc.Item(c.Context(), id)
	if err != nil {
		if stderrors.Is(err, service.ErrNotFound) {
			return response.NotFound(c, errors.ErrNotFound)
		}
		return response.InternalError(c, errors.ErrInternalServer)
	}
	return response.Success(c, item)
}

func (h *PublicHandler) Sources(c fiber.Ctx) error {
	if h.offline(c) {
		return nil
	}
	data, err := h.svc.Sources(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	c.Set("Cache-Control", cacheFeed)
	return response.Success(c, data)
}

func parseCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func parseWorkID(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func parseLimit(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 20, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 50 {
		return 0, false
	}
	return n, true
}
