package handler

import (
	"strconv"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/search"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/imageclient"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// SubmissionHandler handles user-submission endpoints: submit / claim /
// patch-draft / delete-draft / list-mine.
type SubmissionHandler struct {
	submissionSvc *service.SubmissionService
	searchHook    *search.Hook        // optional write-through to Meilisearch
	imgClient     *imageclient.Client // optional; nil disables banner multipart
}

// NewSubmissionHandler creates a SubmissionHandler.
func NewSubmissionHandler(svc *service.SubmissionService, hook *search.Hook, img *imageclient.Client) *SubmissionHandler {
	return &SubmissionHandler{submissionSvc: svc, searchHook: hook, imgClient: img}
}

// Submit handles POST /galgame/submit.
//
// Accepts JSON body or multipart/form-data (with optional `file` banner,
// same pattern as POST /galgame). Creates a status=3 galgame, returns it.
func (h *SubmissionHandler) Submit(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	// Roles decide direct-publish vs review-queue (creator/moderator/admin publish
	// straight to status=0). optionalJWT/jwtAuth populates user_roles.
	roles, _ := c.Locals("user_roles").([]string)

	var req dto.SubmitGalgameRequest
	bannerHash, err := parseGalgameWriteBody(c, h.imgClient, &req)
	if err != nil {
		return mapWriteBodyError(c, err)
	}
	if bannerHash != "" {
		// Multipart-uploaded banner becomes the pinned cover (sort_order=0).
		req.PromoteCoverHash = bannerHash
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	g, err := h.submissionSvc.Submit(c.Context(), int(userID), roles, &req)
	if err != nil {
		return mapSubmissionError(c, err)
	}

	if h.searchHook != nil {
		h.searchHook.Galgame(g.ID)
	}
	return response.Success(c, g)
}

// Claim handles POST /galgame/:gid/claim.
func (h *SubmissionHandler) Claim(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	g, err := h.submissionSvc.Claim(c.Context(), int(userID), gid)
	if err != nil {
		return mapSubmissionError(c, err)
	}

	if h.searchHook != nil {
		h.searchHook.Galgame(g.ID)
	}
	return response.Success(c, g)
}

// PatchDraft handles PATCH /galgame/:gid (submitter-only, drafts only).
func (h *SubmissionHandler) PatchDraft(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.UpdateGalgameRequest
	bannerHash, err := parseGalgameWriteBody(c, h.imgClient, &req)
	if err != nil {
		return mapWriteBodyError(c, err)
	}
	if bannerHash != "" {
		// Multipart-uploaded banner promotes to pinned cover via the
		// overlayUpdate path; PatchDraft uses the same overlay as
		// regular Update so the merge logic is shared.
		req.PromoteCoverHash = bannerHash
	}

	g, err := h.submissionSvc.PatchDraft(c.Context(), int(userID), gid, &req)
	if err != nil {
		return mapSubmissionError(c, err)
	}

	if h.searchHook != nil {
		h.searchHook.Galgame(g.ID)
	}
	return response.Success(c, g)
}

// DeleteDraft handles DELETE /galgame/:gid (submitter-only, drafts only).
func (h *SubmissionHandler) DeleteDraft(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	gid, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	if err := h.submissionSvc.DeleteDraft(c.Context(), int(userID), gid); err != nil {
		return mapSubmissionError(c, err)
	}

	// IMPORTANT: removal, not upsert. searchHook.Galgame() would reload from
	// DB and find nothing → leave the Meilisearch document orphaned, where
	// /galgame/search?include_pending=true would still surface it but
	// /galgame/batch would return empty (the phantom-entry bug).
	if h.searchHook != nil {
		h.searchHook.GalgameDelete(gid)
	}
	return response.Success(c, nil)
}

// ListMine handles GET /galgame/mine.
func (h *SubmissionHandler) ListMine(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	var req dto.ListMineRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	items, total, err := h.submissionSvc.ListMine(c.Context(), int(userID), &req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
	})
}

// mapSubmissionError maps service AppErrors to the appropriate HTTP status
// for the submission endpoints. Falls back to 500 for plain errors.
func mapSubmissionError(c fiber.Ctx, err error) error {
	appErr, ok := err.(*errors.AppError)
	if !ok {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	switch appErr.Code {
	case errors.ErrGalgameNotFound:
		return response.NotFound(c, appErr.Code)
	case errors.ErrGalgameSubmitterOnly:
		return response.Forbidden(c, appErr.Code)
	case errors.ErrGalgameQuotaExceeded:
		return response.Error(c, fiber.StatusTooManyRequests, appErr.Code, appErr.Message)
	default:
		return response.BadRequest(c, appErr.Code)
	}
}
