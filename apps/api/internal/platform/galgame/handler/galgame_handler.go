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

// GalgameHandler handles galgame HTTP requests
type GalgameHandler struct {
	galgameService *service.GalgameService
	searchHook     *search.Hook        // optional write-through to Meilisearch
	imgClient      *imageclient.Client // optional; nil disables banner upload via multipart
}

// NewGalgameHandler creates a new GalgameHandler.
// Pass nil for hook to disable write-through (e.g. in tests).
// Pass nil for imgClient to disable banner-file upload via multipart;
// consumers must then specify the banner via the `covers` array
// (sort_order=0) in the JSON body directly.
func NewGalgameHandler(galgameService *service.GalgameService, hook *search.Hook, imgClient *imageclient.Client) *GalgameHandler {
	return &GalgameHandler{
		galgameService: galgameService,
		searchHook:     hook,
		imgClient:      imgClient,
	}
}

// List returns a paginated list of galgames
func (h *GalgameHandler) List(c fiber.Ctx) error {
	var req dto.ListGalgameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	items, total, err := h.galgameService.List(c.Context(), &req)
	if err != nil {
		// Service surfaces invalid released_from/to (bad YYYY[-MM]) as a
		// *AppError(ErrValidationFailed) — map it to 400 with the parse
		// message so callers see "invalid release month" rather than a
		// generic 500. Any non-AppError is a real internal failure.
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequestMsg(c, appErr.Code, appErr.Message)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"items": items,
		"total": total,
	})
}

// Get returns a galgame by ID with all relations.
//
// content_limit policy: NO default filter ("" passed through) — direct
// URL access by gid is intentional (bookmark, deep link, click-through
// from a SFW-filtered listing). Callers that want to gate detail-page
// access uniformly with the listing they came from should pass the same
// content_limit value explicitly; entries that don't match → 404.
// Diverges from List/search which default to "sfw" — see handbook §16.
func (h *GalgameHandler) Get(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	// Viewer-aware: optionalJWT populates user_id + user_roles when a valid
	// Bearer is present. Lets a submitter open their own pending/declined
	// draft (kungal/moyu /edit/.../draft/<gid>), and an admin/moderator
	// preview any pending submission for review; anonymous sees status=0 only.
	viewerUserID, _ := c.Locals("user_id").(uint)
	viewerRoles, _ := c.Locals("user_roles").([]string)
	contentLimit := utils.ParseContentLimit(c.Query("content_limit"), "")
	galgame, users, err := h.galgameService.GetByIDWithViewer(c.Context(), id, int(viewerUserID), viewerRoles, contentLimit)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.NotFound(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"galgame": galgame,
		"users":   users,
	})
}

// Create creates a new galgame.
//
// Accepts both JSON body and multipart/form-data (with optional `file` field).
// In multipart mode, the file is uploaded to image_service first and the
// resulting hash is promoted to the pinned cover (sort_order=0) of the
// new galgame via PromoteCoverHash — same merge logic as Update/PR.
func (h *GalgameHandler) Create(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}

	var req dto.CreateGalgameRequest
	bannerHash, err := parseGalgameWriteBody(c, h.imgClient, &req)
	if err != nil {
		return mapWriteBodyError(c, err)
	}
	if bannerHash != "" {
		// Multipart-uploaded banner becomes the pinned cover (sort_order=0)
		// via promoteCoverHashInPlace inside buildCreateSnapshot.
		req.PromoteCoverHash = bannerHash
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	galgame, err := h.galgameService.Create(c.Context(), int(userID), &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			return response.BadRequest(c, appErr.Code)
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Galgame(galgame.ID)

	return response.Success(c, galgame)
}

// Update updates an existing galgame.
//
// Accepts both JSON body and multipart/form-data (with optional `file`
// banner). In multipart mode, the file is uploaded to image_service
// first and the resulting hash is promoted to the pinned cover via
// PromoteCoverHash; existing covers are preserved with sort_order
// shifted to keep the partial-unique index satisfied. Goes through the
// standard revision/PR pipeline.
func (h *GalgameHandler) Update(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	roles, _ := c.Locals("user_roles").([]string)

	id, err := strconv.Atoi(c.Params("gid"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	var req dto.UpdateGalgameRequest
	bannerHash, err := parseGalgameWriteBody(c, h.imgClient, &req)
	if err != nil {
		return mapWriteBodyError(c, err)
	}
	if bannerHash != "" {
		// Multipart-uploaded banner promotes to pinned cover. overlayUpdate
		// runs promoteCoverHashInPlace after applying the Covers field, so
		// the upload wins the sort_order=0 slot.
		req.PromoteCoverHash = bannerHash
	}
	// Validate AFTER binding (mirrors Create) so the DTO's dive/len/max tags
	// on links/covers/screenshots actually run — there is no app-wide
	// StructValidator, so without this an empty/garbage image_hash persists.
	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	galgame, err := h.galgameService.Update(c.Context(), int(userID), id, roles, &req)
	if err != nil {
		if appErr, ok := err.(*errors.AppError); ok {
			switch appErr.Code {
			case errors.ErrGalgameNotFound:
				return response.NotFound(c, appErr.Code)
			case errors.ErrGalgameForbidden:
				return response.Forbidden(c, appErr.Code)
			default:
				return response.BadRequest(c, appErr.Code)
			}
		}
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	h.searchHook.Galgame(id)

	return response.Success(c, galgame)
}

// BatchGet returns lightweight galgame info for a list of IDs.
//
// Public by default (status=0 only). When an OptionalJWT middleware has
// populated user_id in Locals, the response also includes the caller's
// own status=3/4 entries — matching the visibility rules in
// docs/galgame_wiki/06-submission-and-review-design.md §6.
//
// content_limit policy: NO default filter ("" passed through) — the
// caller already knows which IDs they want (favorites, references,
// patch.galgame_id), and silently hiding entries they explicitly listed
// would break those consumers. Pass content_limit=sfw to opt INTO
// filtering, =nsfw to receive NSFW-only, =all is equivalent to omitting.
// Diverges from List/search which default to "sfw" — see handbook.
func (h *GalgameHandler) BatchGet(c fiber.Ctx) error {
	var req dto.BatchGetGalgameRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if len(req.IDs) == 0 || len(req.IDs) > 100 {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "ids must contain 1-100 items")
	}

	viewerUserID, _ := c.Locals("user_id").(uint)
	contentLimit := utils.ParseContentLimit(req.ContentLimit, "")
	items, err := h.galgameService.BatchGetWithViewer(c.Context(), req.IDs, int(viewerUserID), contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, items)
}

// UserStats returns aggregated galgame statistics for a user
func (h *GalgameHandler) UserStats(c fiber.Ctx) error {
	userID, err := strconv.Atoi(c.Params("id"))
	if err != nil || userID <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	stats, err := h.galgameService.GetUserStats(c.Context(), userID)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, stats)
}

// UserGalgames returns a user's PUBLISHED galgames (newest first, paginated)
// as briefs — the source for a consumer's profile "已发布 Galgame" tab. Public
// (profiles are publicly viewable); status=0 only, so no viewer gating needed.
func (h *GalgameHandler) UserGalgames(c fiber.Ctx) error {
	userID, err := strconv.Atoi(c.Params("id"))
	if err != nil || userID <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit < 1 || limit > 100 {
		limit = 24
	}
	// Profiles are crawlable → default SFW (handbook §16); callers pass
	// content_limit=all to opt into NSFW.
	contentLimit := utils.ParseContentLimit(c.Query("content_limit"), "sfw")

	items, total, err := h.galgameService.ListUserGalgames(c.Context(), userID, page, limit, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"galgames": items,
		"total":    total,
	})
}

// UserContributedGalgames returns the galgames a user has CONTRIBUTED to
// (created OR edited — the galgame_contributor relation), newest-contribution-
// first, paginated, as briefs — the source for a profile "贡献的 Galgame" tab.
// Public (profiles are publicly viewable); status=0 only + SFW-by-default
// (handbook §16, pass content_limit=all to include NSFW). Distinct from
// UserGalgames, which lists only galgames the user CREATED.
func (h *GalgameHandler) UserContributedGalgames(c fiber.Ctx) error {
	userID, err := strconv.Atoi(c.Params("id"))
	if err != nil || userID <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit < 1 || limit > 100 {
		limit = 24
	}
	contentLimit := utils.ParseContentLimit(c.Query("content_limit"), "sfw")

	items, total, err := h.galgameService.ListContributedGalgames(c.Context(), userID, page, limit, contentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, fiber.Map{
		"galgames": items,
		"total":    total,
	})
}

// CheckVNDB checks if a VNDB ID already exists
func (h *GalgameHandler) CheckVNDB(c fiber.Ctx) error {
	var req dto.CheckVNDBRequest
	if err := c.Bind().Query(&req); err != nil {
		return response.BadRequest(c, errors.ErrBadRequest)
	}

	if err := utils.Validate(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	exists, galgameID, err := h.galgameService.CheckVNDB(c.Context(), req.VNDBID)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	data := fiber.Map{"exists": exists}
	if exists {
		data["galgame_id"] = galgameID
	}

	return response.Success(c, data)
}
