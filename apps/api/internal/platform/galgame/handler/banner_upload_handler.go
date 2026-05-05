package handler

import (
	"log/slog"
	"strconv"

	"api/internal/platform/galgame/model"
	"api/pkg/errors"
	"api/pkg/imageclient"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// BannerUploadHandler accepts a multipart banner file, uploads it to
// image_service via the SDK, and writes `galgame.banner_image_hash`.
//
// Endpoint: POST /api/v1/galgame/:gid/banner   (auth required)
//
// Form fields:
//   - file (required, image/* MIME, sniffed by image_service)
//
// Response (envelope { code, message, data }):
//   data: { hash, url, variant_urls, width, height, size_bytes, deduplicated }
type BannerUploadHandler struct {
	db        *gorm.DB
	imgClient *imageclient.Client
}

func NewBannerUploadHandler(db *gorm.DB, imgClient *imageclient.Client) *BannerUploadHandler {
	return &BannerUploadHandler{db: db, imgClient: imgClient}
}

// Upload handles the multipart upload + DB write.
func (h *BannerUploadHandler) Upload(c fiber.Ctx) error {
	if h.imgClient == nil {
		return response.InternalErrorMsg(c, "image client not configured (KUN_IMAGE_CLIENT_ID/SECRET unset)")
	}

	galgameID, err := strconv.Atoi(c.Params("gid"))
	if err != nil || galgameID <= 0 {
		return response.BadRequest(c, errors.ErrInvalidID)
	}

	// Verify galgame exists before uploading (cheap sanity check).
	var existing model.Galgame
	if err := h.db.WithContext(c.Context()).
		Select("id").
		Where("id = ?", galgameID).
		First(&existing).Error; err != nil {
		return response.NotFound(c, errors.ErrGalgameNotFound)
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

	result, err := h.imgClient.Upload(c.Context(), src, fh.Filename, "galgame_banner")
	if err != nil {
		slog.Error("upload banner failed", "gid", galgameID, "err", err)
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Write hash back. We deliberately only update the new _hash column;
	// the legacy `banner` URL stays untouched so existing render paths
	// continue to work until the frontend switches over.
	if err := h.db.WithContext(c.Context()).
		Model(&model.Galgame{}).
		Where("id = ?", galgameID).
		Updates(map[string]any{
			"banner_image_hash":         result.Hash,
			"banner_migration_status":   1,
			"banner_migration_attempts": gorm.Expr("banner_migration_attempts + 1"),
		}).Error; err != nil {
		slog.Error("persist banner hash failed", "gid", galgameID, "err", err)
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	return response.Success(c, result)
}
