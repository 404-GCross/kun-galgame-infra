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
// Endpoint: POST /api/v1/admin/users/:uuid/avatar  (admin auth)
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

// Upload handles the multipart upload + DB write.
func (h *AvatarUploadHandler) Upload(c fiber.Ctx) error {
	if h.imgClient == nil {
		return response.InternalErrorMsg(c, "image client not configured (KUN_IMAGE_CLIENT_ID/SECRET unset)")
	}

	uuid := c.Params("uuid")
	if uuid == "" {
		return response.BadRequest(c, errors.ErrInvalidID)
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
