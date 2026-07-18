package handler

import (
	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// RevisionHandler serves the surviving galgame merged-revision S2S feed. The
// old-wire revision/PR read+write endpoints retired with apps/wiki at E3b —
// user edits now flow through the editing engine (kungal's edit BFF).
type RevisionHandler struct {
	svc *service.GalgameService
}

// NewRevisionHandler creates a new RevisionHandler.
func NewRevisionHandler(svc *service.GalgameService) *RevisionHandler {
	return &RevisionHandler{svc: svc}
}

// RecentRevisions serves GET /galgame/revisions/recent — the merged-revision
// feed consumed by downstream crons (kungal/moyu) to mirror edit activity into
// their local timelines. Basic-Auth (OAuth client), same as /messages/feed.
func (h *RevisionHandler) RecentRevisions(c fiber.Ctx) error {
	var req dto.RecentRevisionsRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	resp, err := h.svc.ListRecentRevisions(c.Context(), req.SinceID, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, resp)
}
