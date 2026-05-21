package handler

import (
	"errors"
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/service"
	apperr "api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// TaxonomyRevisionHandler exposes the per-entity revision/history/revert
// surface that the four taxonomy handlers (tag / official / engine /
// series) each route into. One file because the endpoint shape is
// identical across entities — the only thing that varies is the
// entity-discriminator string and the per-entity revert path.
type TaxonomyRevisionHandler struct {
	svc *service.TaxonomyService
}

func NewTaxonomyRevisionHandler(svc *service.TaxonomyService) *TaxonomyRevisionHandler {
	return &TaxonomyRevisionHandler{svc: svc}
}

// listRevisions answers GET /<entity>/:id/revisions with newest-first pagination.
// entity MUST be one of the four taxonomy entity discriminators.
func (h *TaxonomyRevisionHandler) listRevisions(c fiber.Ctx, entity string) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	page, _ := strconv.Atoi(c.Query("page", "1"))
	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	items, total, err := h.svc.ListRevisions(c.Context(), entity, id, page, limit)
	if err != nil {
		return response.InternalError(c, apperr.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// getRevision answers GET /<entity>/:id/revisions/:rev. Used by admin UIs
// to render a specific historical snapshot before deciding to revert.
func (h *TaxonomyRevisionHandler) getRevision(c fiber.Ctx, entity string) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	rev, err := strconv.Atoi(c.Params("rev"))
	if err != nil {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	row, err := h.svc.GetRevision(c.Context(), entity, id, rev)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.NotFound(c, apperr.ErrNotFound)
		}
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, row)
}

// revert answers POST /<entity>/:id/revert {revision: N}. The actual
// state-machine logic (resurrect-if-deleted, write a new 'reverted'
// row, etc.) lives in TaxonomyService.Revert<Entity>; this handler
// only binds + dispatches.
func (h *TaxonomyRevisionHandler) revert(c fiber.Ctx, entity string) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		return response.Forbidden(c, apperr.ErrForbidden)
	}
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, apperr.ErrInvalidID)
	}
	// All four entities share the same revert body shape, so reuse the
	// pre-existing dto.RevertRequest defined in revision_dto.go rather
	// than declaring four near-identical per-entity DTOs.
	var req dto.RevertRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, apperr.ErrValidationFailed, err.Error())
	}

	role := roleLevel(roles)
	switch entity {
	case model.TaxonomyEntityTag:
		err = h.svc.RevertTag(c.Context(), int(userID), role, id, req.Revision)
	case model.TaxonomyEntityOfficial:
		err = h.svc.RevertOfficial(c.Context(), int(userID), role, id, req.Revision)
	case model.TaxonomyEntityEngine:
		err = h.svc.RevertEngine(c.Context(), int(userID), role, id, req.Revision)
	case model.TaxonomyEntitySeries:
		err = h.svc.RevertSeries(c.Context(), int(userID), role, id, req.Revision)
	default:
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	if err != nil {
		return mapAppErrOrInternal(c, err)
	}
	return response.Success(c, fiber.Map{"reverted_to": req.Revision})
}

// Per-entity wrappers (so router calls remain readable).

func (h *TaxonomyRevisionHandler) TagListRevisions(c fiber.Ctx) error {
	return h.listRevisions(c, model.TaxonomyEntityTag)
}
func (h *TaxonomyRevisionHandler) TagGetRevision(c fiber.Ctx) error {
	return h.getRevision(c, model.TaxonomyEntityTag)
}
func (h *TaxonomyRevisionHandler) TagRevert(c fiber.Ctx) error {
	return h.revert(c, model.TaxonomyEntityTag)
}

func (h *TaxonomyRevisionHandler) OfficialListRevisions(c fiber.Ctx) error {
	return h.listRevisions(c, model.TaxonomyEntityOfficial)
}
func (h *TaxonomyRevisionHandler) OfficialGetRevision(c fiber.Ctx) error {
	return h.getRevision(c, model.TaxonomyEntityOfficial)
}
func (h *TaxonomyRevisionHandler) OfficialRevert(c fiber.Ctx) error {
	return h.revert(c, model.TaxonomyEntityOfficial)
}

func (h *TaxonomyRevisionHandler) EngineListRevisions(c fiber.Ctx) error {
	return h.listRevisions(c, model.TaxonomyEntityEngine)
}
func (h *TaxonomyRevisionHandler) EngineGetRevision(c fiber.Ctx) error {
	return h.getRevision(c, model.TaxonomyEntityEngine)
}
func (h *TaxonomyRevisionHandler) EngineRevert(c fiber.Ctx) error {
	return h.revert(c, model.TaxonomyEntityEngine)
}

func (h *TaxonomyRevisionHandler) SeriesListRevisions(c fiber.Ctx) error {
	return h.listRevisions(c, model.TaxonomyEntitySeries)
}
func (h *TaxonomyRevisionHandler) SeriesGetRevision(c fiber.Ctx) error {
	return h.getRevision(c, model.TaxonomyEntitySeries)
}
func (h *TaxonomyRevisionHandler) SeriesRevert(c fiber.Ctx) error {
	return h.revert(c, model.TaxonomyEntitySeries)
}

// roleLevel maps the OAuth role names that flow in via JWT (`user`,
// `moderator`, `admin`) to the integer recorded on the taxonomy
// revision (admin=3 / moderator=2 / user=1 / 0 fallback). Stored so
// future field-level RBAC can fetch the role at edit time even if the
// user's role has since changed.
func roleLevel(roles []string) int {
	level := 0
	for _, r := range roles {
		switch r {
		case "admin":
			if level < 3 {
				level = 3
			}
		case "moderator":
			if level < 2 {
				level = 2
			}
		case "user":
			if level < 1 {
				level = 1
			}
		}
	}
	return level
}

// mapAppErrOrInternal lets handlers return the right HTTP status when
// the service surfaced a typed *apperr.AppError (NotFound /
// Forbidden / Validation) vs a generic internal failure. Keeps the
// taxonomy handlers consistent without ad-hoc error switches.
func mapAppErrOrInternal(c fiber.Ctx, err error) error {
	var ae *apperr.AppError
	if errors.As(err, &ae) {
		switch ae.Code {
		case apperr.ErrNotFound, apperr.ErrGalgameNotFound:
			return response.NotFound(c, ae.Code)
		case apperr.ErrBadRequest, apperr.ErrInvalidID, apperr.ErrValidationFailed:
			return response.BadRequestMsg(c, ae.Code, ae.Message)
		case apperr.ErrForbidden, apperr.ErrGalgameForbidden:
			return response.Forbidden(c, ae.Code)
		case apperr.ErrAuthUnauthorized:
			return response.Unauthorized(c, ae.Code)
		}
	}
	return response.InternalError(c, apperr.ErrOperationFailed)
}

