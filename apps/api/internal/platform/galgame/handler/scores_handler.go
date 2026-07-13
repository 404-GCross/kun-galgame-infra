package handler

import (
	"fmt"
	"strconv"

	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// Cross-source read face (step 34). Both endpoints are SERVED here by Fiber and
// registered spec-only on Huma (galgame_read_huma.go) so the OpenAPI derives
// from the same dto.* types — like the calendar. Stats keeps the ETag /
// If-None-Match conditional cache (calendar_handler.go precedent) that Huma's
// typed model doesn't fit.

// Scores serves GET /galgame/:gid/scores — the three-source (VNDB / Bangumi /
// EG) rating snapshot for one galgame, each source carrying a backend-composed
// attribution URL (P-★ narrow slice). Missing sources are null; a galgame with
// no rows returns all-null. No visibility gate — external score indicators are
// display-only.
func (h *GalgameHandler) Scores(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	scores, err := h.galgameService.GetScores(c.Context(), gid)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, scores)
}

// Stats serves GET /galgame/stats — the site-wide cross-source statistics
// overview (the six frozen galgame_stats snapshots, payloads passed through
// verbatim). ETag is folded from max(built_at); the stats rebuild daily
// (05:45), so a short shared-cache window with cheap ETag revalidation keeps an
// edit from being served stale. Cache-Control is the calendar family.
func (h *GalgameHandler) Stats(c fiber.Ctx) error {
	data, maxBuilt, err := h.galgameService.GetStats(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	etag := fmt.Sprintf(`W/"gstats-%d"`, maxBuilt.Unix())
	c.Set("ETag", etag)
	c.Set("Cache-Control", "public, max-age=0, s-maxage=3600, stale-while-revalidate=300")
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}

	return response.Success(c, data)
}
