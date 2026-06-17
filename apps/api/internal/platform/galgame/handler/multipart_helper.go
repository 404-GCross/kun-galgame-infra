package handler

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"strings"

	apperr "api/pkg/errors"
	"api/pkg/imageclient"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// errMultipartMissingData is returned when a multipart request lacks the
// required `data` form field.
var errMultipartMissingData = stderrors.New("multipart request missing 'data' form field")

// errImageClientUnconfigured is returned when a multipart request carries a
// banner `file` but the image_service client isn't wired up
// (KUN_IMAGE_CLIENT_ID/SECRET unset). Distinct sentinel so mapWriteBodyError
// can surface an actionable message instead of a generic "请求格式错误",
// which misleads callers into thinking their payload is malformed.
var errImageClientUnconfigured = stderrors.New("image client not configured (KUN_IMAGE_CLIENT_ID/SECRET unset)")

// parseGalgameWriteBody parses Create/Update/SubmitPR request bodies in
// either of two equivalent formats:
//
//  1. application/json — standard JSON request body. Used by API consumers
//     that don't need to upload a banner file.
//
//  2. multipart/form-data — used by the wiki frontend when the user wants
//     to upload a new banner file in the same edit operation:
//     data: <JSON> (required)  — same payload as JSON mode
//     file: <binary> (optional) — banner image
//     If the file part is present, this helper streams it to image_service
//     via imageclient.Upload(preset=galgame_banner) and returns the resulting
//     hash. Caller assigns the hash to its DTO's PromoteCoverHash field;
//     the service then merges it as sort_order=0 into the eventual
//     covers[] snapshot (see service.promoteCoverHashInPlace) so it
//     gets persisted as the pinned banner in the same transaction.
//
// Returns:
//   - bannerHash: the new banner hash from image_service, or "" if no file
//   - err: parse / validation / upload error
//
// Errors from imageclient.Upload (quota, moderation, upload-disabled) are
// returned as *imageclient.Error so the caller can surface them with the
// proper status code instead of a generic 500.
func parseGalgameWriteBody(c fiber.Ctx, imgClient *imageclient.Client, target any) (bannerHash string, err error) {
	contentType := c.Get(fiber.HeaderContentType)

	if !strings.HasPrefix(contentType, fiber.MIMEMultipartForm) {
		// JSON mode — parse the raw body as JSON. Bind().JSON returns the
		// usual fiber error wrapped inside; any failure should map to 400.
		if err := c.Bind().JSON(target); err != nil {
			return "", fmt.Errorf("parse json body: %w", err)
		}
		return "", nil
	}

	// Multipart mode.
	dataStr := c.FormValue("data")
	if dataStr == "" {
		return "", errMultipartMissingData
	}
	if err := json.Unmarshal([]byte(dataStr), target); err != nil {
		return "", fmt.Errorf("parse 'data' field as json: %w", err)
	}

	fh, _ := c.FormFile("file")
	if fh == nil {
		// No file — same as JSON mode at this point.
		return "", nil
	}

	if imgClient == nil {
		return "", errImageClientUnconfigured
	}

	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("open uploaded file: %w", err)
	}
	defer src.Close()

	result, err := imgClient.Upload(c.Context(), src, fh.Filename, "galgame_banner")
	if err != nil {
		// Returned as-is (possibly *imageclient.Error) so caller can
		// inspect the upstream status code.
		return "", err
	}
	return result.Hash, nil
}

// mapWriteBodyError converts errors from parseGalgameWriteBody into
// appropriate HTTP responses. Distinguishes between client mistakes
// (bad JSON, missing field) and image_service propagation (quota,
// upload-disabled) so the frontend can branch on the error code.
func mapWriteBodyError(c fiber.Ctx, err error) error {
	// image_service propagation: pass through original status + code so
	// the frontend can show a specific message (e.g. "上传暂未开放" when
	// image_service returns 80015).
	if e, ok := err.(*imageclient.Error); ok {
		slog.Warn("image_service rejected upload",
			"status", e.StatusCode, "code", e.Code, "msg", e.Message)
		return response.Error(c, e.StatusCode, e.Code, e.Message)
	}

	if stderrors.Is(err, errMultipartMissingData) {
		return response.BadRequestMsg(c, apperr.ErrMissingParam, err.Error())
	}

	// Banner upload attempted but image_service isn't configured. This is
	// an operational/deployment gap, not a client payload problem — say so
	// explicitly so the user knows to submit without a preview image (or an
	// operator knows to set KUN_IMAGE_CLIENT_ID/SECRET) instead of chasing
	// a nonexistent format bug.
	if stderrors.Is(err, errImageClientUnconfigured) {
		slog.Error("banner upload requested but image_service unconfigured", "err", err)
		return response.BadRequestMsg(c, apperr.ErrBadRequest,
			"图片服务未配置, 暂时无法上传预览图; 请不带预览图重新提交, 或联系管理员部署 image_service")
	}

	// Anything else (JSON parse failure, file open failure) → 400.
	slog.Warn("parse galgame write body failed", "err", err)
	return response.BadRequest(c, apperr.ErrBadRequest)
}

// allowedGalgameUploadPresets gates the standalone upload endpoint to the two
// galgame image presets (both land under site=galgame_wiki).
var allowedGalgameUploadPresets = map[string]struct{}{
	"galgame_banner":     {},
	"galgame_screenshot": {},
}

// UploadImage is the canonical, single entry point for galgame image bytes
// (covers + screenshots) across the whole ecosystem. A logged-in user POSTs
// multipart {file, preset}; it uploads to image_service under the WIKI's image
// client (site=galgame_wiki) and returns the content hash, which the caller
// references in covers[]/screenshots[] on a later Create/Update/PR.
//
// forum and moyu proxy their users' cover/screenshot uploads here (via their
// server-to-server wiki client, forwarding the user's JWT) instead of
// uploading under their own image_service sites. That way EVERY galgame image
// is owned by site=galgame_wiki, so the single site-scoped galgame
// reference-ping keeps them all alive and the wiki is the one source of truth
// for galgame image bytes — not just the galgame rows.
func (h *GalgameHandler) UploadImage(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return response.Unauthorized(c, apperr.ErrAuthUnauthorized)
	}
	if h.imgClient == nil {
		return response.BadRequestMsg(c, apperr.ErrBadRequest,
			"图片服务未配置, 无法上传 galgame 图片 (KUN_IMAGE_CLIENT_ID/SECRET 未设置)")
	}

	preset := c.FormValue("preset")
	if _, ok := allowedGalgameUploadPresets[preset]; !ok {
		return response.BadRequestMsg(c, apperr.ErrBadRequest,
			"preset 必须是 galgame_banner 或 galgame_screenshot")
	}

	fh, err := c.FormFile("file")
	if err != nil || fh == nil {
		return response.BadRequestMsg(c, apperr.ErrMissingParam, "缺少上传文件 file")
	}
	src, err := fh.Open()
	if err != nil {
		return response.BadRequest(c, apperr.ErrBadRequest)
	}
	defer src.Close()

	result, err := h.imgClient.Upload(c.Context(), src, fh.Filename, preset)
	if err != nil {
		// image_service propagation (quota / moderation / upload-disabled):
		// pass through status + code so callers can branch on it.
		if e, ok := err.(*imageclient.Error); ok {
			slog.Warn("galgame image upload rejected by image_service",
				"status", e.StatusCode, "code", e.Code, "msg", e.Message, "preset", preset)
			return response.Error(c, e.StatusCode, e.Code, e.Message)
		}
		slog.Error("galgame image upload failed", "err", err, "preset", preset)
		return response.InternalError(c, apperr.ErrOperationFailed)
	}

	// Return the full UploadResult (hash, url, variant_urls, width, height,
	// size_bytes, deduplicated) so downstream proxies (forum/moyu) keep the
	// same response shape their FE already consumes.
	return response.Success(c, result)
}
