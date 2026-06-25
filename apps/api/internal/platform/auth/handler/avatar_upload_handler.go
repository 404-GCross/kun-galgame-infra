package handler

import (
	"log/slog"

	"api/internal/platform/auth/model"
	"api/pkg/errors"
	"api/pkg/imageclient"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// AvatarUploadHandler accepts a multipart avatar file, uploads it to
// image_service via the SDK, and writes `users.avatar_image_hash`.
//
// Two endpoints share the same backing logic, differing only in how
// the target user UUID is resolved:
//
//   - POST /api/v1/admin/users/:uuid/avatar  (admin auth, uuid from path)
//   - POST /api/v1/auth/me/avatar            (end-user JWT, uuid from token)
//
// Form fields:
//   - file (required, image/* MIME)
//
// Response: { hash, url, variant_urls (256/100), width, height, size_bytes, deduplicated }
type AvatarUploadHandler struct {
	db        *gorm.DB
	imgClient *imageclient.Client
}

func NewAvatarUploadHandler(db *gorm.DB, imgClient *imageclient.Client) *AvatarUploadHandler {
	return &AvatarUploadHandler{db: db, imgClient: imgClient}
}

// Upload is the admin variant — uuid comes from the route param.
func (h *AvatarUploadHandler) Upload(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	return h.uploadFor(c, uuid)
}

// UploadMine is the end-user variant — uuid comes from the JWT claims
// set by middleware.Auth. The user is implicitly authorised to change
// their own avatar; no admin role required.
func (h *AvatarUploadHandler) UploadMine(c fiber.Ctx) error {
	uuid, _ := c.Locals("user_uuid").(string)
	if uuid == "" {
		// Middleware.Auth guarantees this is set on the protected route,
		// but be defensive in case of a wiring regression.
		return response.Unauthorized(c, errors.ErrAuthUnauthorized)
	}
	return h.uploadFor(c, uuid)
}

// UploadClientLogo uploads an OAuth-client logo/icon to image_service and
// returns the result ({hash, url, …}) WITHOUT touching any client row — the
// admin form saves the returned `url` into oauth_clients.logo_url via the
// normal create/update call. Decoupling the upload from the client id lets the
// same flow work at create time (no id yet) and edit time alike.
//
// Uses the account image client's only allowed preset ("avatar": square
// 256/100), which is exactly what an app icon wants. Admin-only route.
func (h *AvatarUploadHandler) UploadClientLogo(c fiber.Ctx) error {
	if h.imgClient == nil {
		return response.InternalErrorMsg(c, "image client not configured (KUN_IMAGE_CLIENT_ID/SECRET unset)")
	}

	fh, err := c.FormFile("file")
	if err != nil || fh == nil {
		return response.BadRequest(c, errors.ErrMissingParam)
	}
	src, err := fh.Open()
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	defer src.Close()

	result, err := h.imgClient.Upload(c.Context(), src, fh.Filename, "avatar")
	if err != nil {
		slog.Error("upload client logo failed", "err", err)
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, result)
}

// uploadFor performs the multipart receive + image_service upload + DB
// write for the given user uuid. Common to both Upload and UploadMine.
func (h *AvatarUploadHandler) uploadFor(c fiber.Ctx, uuid string) error {
	if h.imgClient == nil {
		return response.InternalErrorMsg(c, "image client not configured (KUN_IMAGE_CLIENT_ID/SECRET unset)")
	}

	// Verify user exists.
	var existing model.User
	if err := h.db.WithContext(c.Context()).
		Select("id").
		Where("uuid = ?", uuid).
		First(&existing).Error; err != nil {
		return response.NotFound(c, errors.ErrAuthUserNotFound)
	}

	fh, err := c.FormFile("file")
	if err != nil || fh == nil {
		return response.BadRequest(c, errors.ErrMissingParam)
	}
	src, err := fh.Open()
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	defer src.Close()

	result, err := h.imgClient.Upload(c.Context(), src, fh.Filename, "avatar")
	if err != nil {
		slog.Error("upload avatar failed", "uuid", uuid, "err", err)
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Set avatar_image_hash. We deliberately leave the legacy `avatar`
	// URL field untouched — for old users this stays as their existing
	// URL, for new users it'll be empty (which is fine).
	if err := h.db.WithContext(c.Context()).
		Model(&model.User{}).
		Where("uuid = ?", uuid).
		Update("avatar_image_hash", result.Hash).Error; err != nil {
		slog.Error("persist avatar hash failed", "uuid", uuid, "err", err)
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, result)
}
