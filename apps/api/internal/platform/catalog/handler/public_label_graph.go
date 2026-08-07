// public_label_graph.go — GET /v1/catalog/labels/{id}/relation-graph (wave
// 188): the whole corporate family around a label in one call.
package handler

import (
	"strconv"

	"api/internal/platform/catalog/model"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// LabelRelationGraph serves GET /v1/catalog/labels/{id}/relation-graph.
//
// It rides the same public face and the same middleware chain as labels/{id},
// and shares its miss semantics: an unknown id is a 404 and a MERGED id is the
// 301 the labels lane already serves, so a consumer holding a stale id learns
// where the identity went here too instead of only on the detail route.
//
// nsfw is the one parameter, and it means what it means everywhere else on this
// face: whether r18 works count toward each node's work_count. There is no
// pagination — the answer is capped by construction (depth and node count), and
// a partial graph paged in slices would not be a graph.
func (h *PublicHandler) LabelRelationGraph(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	graph, found, err := h.svc.LabelRelationGraph(c.Context(), id, nsfwQuery(c))
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return h.missOrMoved(c, model.EntityTypeLabel, "labels", id)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, graph)
}
