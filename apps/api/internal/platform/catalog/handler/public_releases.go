package handler

import (
	stderrors "errors"
	"strings"

	"api/internal/platform/catalog/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

const (
	msgBadReleaseSort  = "sort must be date_desc|date_asc"
	msgBadReleaseKind  = "kind must be a comma-separated subset of default, digital, physical, trial, patch"
	msgBadReleaseDate  = "date_from and date_to must be YYYY-MM-DD"
	msgBadOfficialFlag = "official must be true|false"
)

func (h *PublicHandler) Releases(c fiber.Ctx) error {
	f := service.ReleaseFeedFilter{
		NSFW:     nsfwQuery(c),
		OLang:    parsePublicOLang(c.Query("olang")),
		Langs:    parseOpenList(c.Query("lang")),
		Platform: strings.TrimSpace(c.Query("platform")),
		Include:  service.ParseWorksListInclude(c.Query("include")),
	}
	switch sort := strings.TrimSpace(c.Query("sort")); sort {
	case "", service.ReleaseFeedSortDateDesc:
		f.Sort = service.ReleaseFeedSortDateDesc
	case service.ReleaseFeedSortDateAsc:
		f.Sort = service.ReleaseFeedSortDateAsc
	default:
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadReleaseSort)
	}
	kinds, ok := releaseKindsPub(c.Query("kind"))
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadReleaseKind)
	}
	f.Kinds = kinds
	if f.Official, ok = officialPub(c.Query("official")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadOfficialFlag)
	}
	if f.DateFrom, ok = datePub(c.Query("date_from")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadReleaseDate)
	}
	if f.DateTo, ok = datePub(c.Query("date_to")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadReleaseDate)
	}
	if f.DisplayLimits, ok = displayLimitsPub(c.Query("content_limit")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadDisplayLimit)
	}
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}

	count, maxCreated, maxID, err := h.svc.ReleaseFeedMeta(c.Context(), f)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	etag := service.ReleaseFeedETag(f.PopulationKey(), count, maxCreated, maxID)
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheSearch)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}

	data, err := h.svc.ReleaseFeed(c.Context(), f, c.Query("cursor"), limit)
	if err != nil {
		if stderrors.Is(err, service.ErrBadCursor) {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadCursor)
		}
		return response.InternalError(c, errors.ErrInternalServer)
	}
	data.Count = count
	return response.Success(c, data)
}

func releaseKindsPub(raw string) ([]int16, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]int16, 0, len(parts))
	seen := make(map[int16]struct{}, len(parts))
	for _, p := range parts {
		k, ok := service.ReleaseKindFromKey(strings.TrimSpace(p))
		if !ok {
			return nil, false
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, true
}

func officialPub(raw string) (*bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return nil, true
	case "true":
		v := true
		return &v, true
	case "false":
		v := false
		return &v, true
	default:
		return nil, false
	}
}

func parseOpenList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "all" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(trimmed, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
