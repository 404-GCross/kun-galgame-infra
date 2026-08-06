// public_releases.go — GET /v1/catalog/releases, the release-grain
// new-releases timeline on the frozen /v1/catalog public projection (wave 174).
//
// The calendar next door is work-grain and shows a title once, in the month of
// its EARLIEST release; this lane is release-grain and shows every dated
// release row on its own date, so ports and re-editions are finally visible.
// Everything the two lanes share is shared literally: the same parsePublicOLang
// (curated ja+zh default, OPEN vocabulary), the same content_limit CLOSED
// vocabulary and 400 message, the same limit posture, the same cheap-meta →
// mint ETag → 304 short-circuit → page pipeline, the same cacheSearch header.
//
// The vocabulary postures, spelled out once because this face mixes all three:
//
//	CLOSED (unknown token → 400): sort, kind, official, content_limit
//	OPEN   (unknown token → empty feed): olang, lang, platform
//	IGNORED (unknown token → dropped): include
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

// Releases serves GET /v1/catalog/releases — every dated release of the LIVE
// galgame registry, newest first by default, keyset-paged over (date, id).
func (h *PublicHandler) Releases(c fiber.Ctx) error {
	f := service.ReleaseFeedFilter{
		NSFW:  nsfwQuery(c),
		OLang: parsePublicOLang(c.Query("olang")),
		// lang / platform are OPEN vocabularies (upstream BCP-47 tags and vndb
		// platform codes), so an unrecognized value yields an empty feed rather
		// than a 400 — the works-list posture for `platform`, verbatim.
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
	// content_limit: a CLOSED vocabulary, so an unknown token is a 400 — the
	// deliberate opposite of olang / lang / platform above. It gates membership,
	// so it also rides in the ETag's population key.
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

// releaseKindsPub reads the comma-separated kind= filter: the CLOSED release
// vocabulary (the five strings the items' `kind` prints). Absent/empty → nil,
// which the service resolves to DefaultReleaseFeedKinds — the FULL-release set,
// i.e. trial and patch excluded. ok=false — a LOUD 400 — on any token outside
// the vocabulary, the claimStatesPub / displayLimitsPub posture verbatim: a
// silently-dropped kind token would answer 200 with the demos the caller asked
// to exclude.
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
			continue // asking for digital twice is asking for digital
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, true
}

// officialPub reads the official= tri-state: absent → nil (no gate), true/false
// → the gate. A CLOSED vocabulary — and deliberately NOT the loose truthiness
// nsfwQuery accepts, because here a typo must not silently mean "false" and
// hand the caller a feed of nothing but fan translations.
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

// parseOpenList reads a comma-separated OPEN vocabulary (lang=): nil for
// absent, for `all` and for an all-blank list — all three mean "no gate" —
// otherwise the trimmed set. No validation by construction: the values are
// upstream tags, so an unknown one is an empty feed, never a 400.
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
