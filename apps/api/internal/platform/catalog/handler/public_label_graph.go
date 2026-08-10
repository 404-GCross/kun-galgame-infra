package handler

import (
	"strconv"

	"api/internal/platform/catalog/model"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

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
