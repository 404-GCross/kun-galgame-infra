package handler

import (
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"api/internal/middleware"
	"api/internal/platform/auth/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// UserBatchHandler serves `GET /api/v1/users/batch` for cross-service
// consumers (kungal / moyu / galgame_wiki backends).
//
// Auth is OAuth Client Basic Auth (see middleware.OAuthClientBasicAuth).
type UserBatchHandler struct {
	svc *service.UserBatchService
}

func NewUserBatchHandler(svc *service.UserBatchService) *UserBatchHandler {
	return &UserBatchHandler{svc: svc}
}

// Get handles GET /users/batch?ids=1,2,3.
//
// Query: ids — required, 1..100 comma-separated unsigned integers.
//
// Response: { users: [...UserBrief], not_found: [id, id, ...] }.
func (h *UserBatchHandler) Get(c fiber.Ctx) error {
	idsStr := c.Query("ids")
	ids, err := parseUintList(idsStr)
	if err != nil || len(ids) == 0 {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "ids is required (comma-separated unsigned integers)")
	}
	if len(ids) > 100 {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "ids: max 100 per request")
	}

	// site_roles in each brief are scoped to the REQUESTING client's site.
	var siteID uint
	if client := middleware.OAuthClientFromCtx(c); client != nil && client.SiteID != nil {
		siteID = *client.SiteID
	}

	resp, err := h.svc.GetBriefs(c.Context(), ids, siteID)
	if err != nil {
		slog.Error("users batch query", "ids_len", len(ids), "err", err)
		return response.InternalError(c, errors.ErrInternalServer)
	}
	return response.Success(c, resp)
}

// Search handles GET /users/search?q=<name>&limit=<n>.
//
// Query: q     — required, 1..50 chars after trim. Case-insensitive substring.
//
//	limit — optional, default 20, capped at 50.
//
// Response: { users: [...UserBrief] }.
func (h *UserBatchHandler) Search(c fiber.Ctx) error {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "q is required")
	}
	// Count runes, not bytes: a 17-char (the username max) all-CJK name is
	// 51 bytes, which len() would wrongly reject.
	if utf8.RuneCountInString(q) > 50 {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "q: max 50 chars")
	}

	limit := 20
	if s := c.Query("limit"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "limit must be a positive integer")
		}
		limit = v
	}
	if limit > 50 {
		limit = 50
	}

	resp, err := h.svc.SearchByName(c.Context(), q, limit)
	if err != nil {
		slog.Error("users search", "q", q, "limit", limit, "err", err)
		return response.InternalError(c, errors.ErrInternalServer)
	}
	return response.Success(c, resp)
}

// parseUintList parses "1,2,3" → []uint{1,2,3}. Empty token / non-numeric
// values cause an error so the caller knows the input was bad rather than
// silently dropping them.
func parseUintList(s string) ([]uint, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, uint(v))
	}
	return out, nil
}
