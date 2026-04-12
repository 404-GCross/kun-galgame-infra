package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// RevisionHandler handles revision and PR HTTP requests
type RevisionHandler struct {
	svc *service.GalgameService
}

// NewRevisionHandler creates a new RevisionHandler
func NewRevisionHandler(svc *service.GalgameService) *RevisionHandler {
	return &RevisionHandler{svc: svc}
}

// ListRevisions returns revision history
func (h *RevisionHandler) ListRevisions(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.ListRevisionRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	items, total, err := h.svc.ListRevisions(c.Context(), gid, req.Page, req.Limit, req.IncludeMinor)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// GetRevision returns a specific revision's snapshot
func (h *RevisionHandler) GetRevision(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	rev, err := strconv.Atoi(c.Params("rev"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	revision, err := h.svc.GetRevision(c.Context(), gid, rev)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, revision)
}

// GetRevisionDiff returns the diff between a revision and its predecessor
func (h *RevisionHandler) GetRevisionDiff(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	rev, err := strconv.Atoi(c.Params("rev"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	changedKeys, oldSnapshot, newSnapshot, err := h.svc.GetRevisionDiff(c.Context(), gid, rev)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"changed_keys": changedKeys,
		"old":          oldSnapshot,
		"new":          newSnapshot,
	})
}

// Revert rolls back to a specific revision
func (h *RevisionHandler) Revert(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.RevertRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	if err := h.svc.Revert(c.Context(), int(uid), gid, req.Revision, roles); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			switch appErr.Code {
			case errors.ErrGalgameForbidden:
				return response.Forbidden(c, appErr.Code)
			default:
				return response.BadRequest(c, appErr.Code)
			}
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, nil)
}

// ListPRs returns PRs for a galgame
func (h *RevisionHandler) ListPRs(c fiber.Ctx) error {
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.ListPRRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	items, total, err := h.svc.ListPRs(c.Context(), gid, req.Page, req.Limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{"items": items, "total": total})
}

// GetPR returns a PR with its diff against the base revision
func (h *RevisionHandler) GetPR(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	pr, err := h.svc.GetPR(c.Context(), id)
	if err != nil {
		return response.NotFound(c, errors.ErrNotFound)
	}

	// Compute diff against base revision
	baseRev, baseErr := h.svc.GetRevision(c.Context(), pr.GalgameID, pr.BaseRevision)
	var changedKeys map[string]bool
	if baseErr == nil {
		baseSnapshot, _ := model.SnapshotFromJSON(baseRev.Snapshot)
		prSnapshot, _ := model.SnapshotFromJSON(pr.Snapshot)
		if baseSnapshot != nil && prSnapshot != nil {
			changedKeys = model.ChangedKeys(baseSnapshot, prSnapshot)
		}
	}

	return response.Success(c, fiber.Map{
		"pr":           pr,
		"changed_keys": changedKeys,
	})
}

// SubmitPR creates a new PR
func (h *RevisionHandler) SubmitPR(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.SubmitPRRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	// Get current snapshot to apply PR changes onto
	latestRev, err := h.svc.GetRevision(c.Context(), gid, 0) // 0 = latest
	if err != nil {
		// Try getting latest from the revision repo directly
		galgame, _, getErr := h.svc.GetByID(c.Context(), gid)
		if getErr != nil {
			return response.NotFound(c, errors.ErrGalgameNotFound)
		}
		baseSnapshot := model.TakeSnapshot(galgame)
		proposedSnapshot := req.ApplyToSnapshot(baseSnapshot)
		pr, submitErr := h.svc.SubmitPR(c.Context(), int(uid), gid, proposedSnapshot, req.Note)
		if submitErr != nil {
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		return response.Success(c, pr)
	}

	baseSnapshot, err := model.SnapshotFromJSON(latestRev.Snapshot)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	proposedSnapshot := req.ApplyToSnapshot(baseSnapshot)

	pr, err := h.svc.SubmitPR(c.Context(), int(uid), gid, proposedSnapshot, req.Note)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, pr)
}

// MergePR merges a PR
func (h *RevisionHandler) MergePR(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.svc.MergePR(c.Context(), int(uid), id, roles); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		// Field conflict or already processed
		return response.BadRequestMsg(c, errors.ErrOperationFailed, err.Error())
	}

	return response.Success(c, nil)
}

// DeclinePR declines a PR
func (h *RevisionHandler) DeclinePR(c fiber.Ctx) error {
	uid, _ := c.Locals("user_uid").(uint)
	if uid == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.svc.DeclinePR(c.Context(), int(uid), id, roles); err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.BadRequestMsg(c, errors.ErrOperationFailed, err.Error())
	}

	return response.Success(c, nil)
}
