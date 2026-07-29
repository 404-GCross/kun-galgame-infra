// galgame_meta_handler.go — the ownership-meta batch read on the surviving
// `/internal` platform-workflow face (A2-1e area B; doc 106 §26 R2 ①).
//
// The forum's edit lane answers two questions about a galgame it does not own
// a copy of: *may this user edit it* (owner assertion) and *who gets notified*
// (the same user). It answers both today by reading the ANONYMOUS published-only
// batch endpoint — which silently returns nothing for any entry in status
// {2,3,4}, so the owner assertion degrades to "not the owner" and the true
// owner is locked out of editing, reverting and reviewing their own unpublished
// entry.
//
// This op is the honest supply for that question: STATUS-BLIND, credentialed,
// and carrying only `{gid, user_id, status}`. `status` rides along so the caller
// can tell "not the owner" from "unpublished" instead of inferring it from an
// absence.
//
// It is deliberately NOT a brief: no titles, no cover, no intro. Ownership is
// not content, and a lane that answers "who owns this" must not become a way to
// read unpublished bodies. The R2 red line also stays intact — this is the
// SURVIVING wiki face, where the wiki's own state machine legitimately lives;
// none of it crosses into the catalog public contract.
package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// metaBatchLimit caps the ids per call, matching the batch face's own limit.
// Over-limit is a 400, never a silent truncation — a truncated ownership answer
// reads as "you are not the owner", the worst possible failure for this lane.
const metaBatchLimit = 100

// GalgameMetaHandler serves the ownership-meta batch read.
type GalgameMetaHandler struct {
	repo *repository.GalgameRepository
}

// NewGalgameMetaHandler wires the meta handler over the galgame repository.
func NewGalgameMetaHandler(repo *repository.GalgameRepository) *GalgameMetaHandler {
	return &GalgameMetaHandler{repo: repo}
}

// Meta serves GET /internal/galgame/meta?ids=1,2,3 — `{items:[{gid,user_id,
// status}]}`, status-blind, order by gid ASC. Ids that do not resolve are
// simply absent (a deleted galgame is not an error), so the caller distinguishes
// "no such entry" from "not the owner" by presence.
func (h *GalgameMetaHandler) Meta(c fiber.Ctx) error {
	raw := strings.TrimSpace(c.Query("ids"))
	if raw == "" {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "ids is required (1-100 galgame ids)")
	}
	parts := strings.Split(raw, ",")
	if len(parts) > metaBatchLimit {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "at most 100 ids")
	}
	ids := make([]int64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || id <= 0 {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "ids must be positive integers")
		}
		ids = append(ids, id)
	}
	rows, err := h.repo.FindMetaByIDs(c.Context(), ids)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if rows == nil {
		rows = []repository.GalgameMetaRow{}
	}
	return response.Success(c, fiber.Map{"items": rows})
}
