package handler

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"strconv"

	"api/pkg/errors"
	"api/pkg/imageclient"

	"github.com/gofiber/fiber/v3"
)

var editImagePresets = map[string]string{
	"galgame_banner":     "catalog_cover",
	"galgame_screenshot": "catalog_screenshot",
}

type EditImageUpload func(ctx context.Context, r io.Reader, filename, preset, uploaderSub string) (*imageclient.UploadResult, error)

func SetupUserEditImages(app *fiber.App, upload EditImageUpload) {
	app.Post(UserPrefix+"/edit/images", editImageHandler(upload))
}

func editImageHandler(upload EditImageUpload) fiber.Handler {
	return func(c fiber.Ctx) error {
		if upload == nil {
			return c.Status(http.StatusServiceUnavailable).JSON(Envelope[any]{
				Code: errors.ErrOperationFailed, Message: "image client not configured",
			})
		}
		preset, ok := editImagePresets[c.FormValue("preset")]
		if !ok {
			return c.Status(http.StatusBadRequest).JSON(Envelope[any]{
				Code: errors.ErrInvalidParam, Message: "preset must be galgame_banner or galgame_screenshot",
			})
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return c.Status(http.StatusBadRequest).JSON(Envelope[any]{
				Code: errors.ErrInvalidParam, Message: "multipart file field is required",
			})
		}
		sub := ""
		if uid, _ := c.Locals("user_id").(uint); uid > 0 {
			sub = "kungal:" + strconv.FormatUint(uint64(uid), 10)
		}
		f, err := fh.Open()
		if err != nil {
			return c.Status(http.StatusBadRequest).JSON(Envelope[any]{
				Code: errors.ErrInvalidParam, Message: "unreadable multipart file",
			})
		}
		defer f.Close()

		res, err := upload(c.Context(), f, fh.Filename, preset, sub)
		if err != nil {
			status, msg := http.StatusBadGateway, "image service upload failed"
			switch {
			case stderrors.Is(err, imageclient.ErrQuotaExceeded):
				status, msg = http.StatusBadRequest, "image quota exceeded"
			case stderrors.Is(err, imageclient.ErrModerationRejected):
				status, msg = http.StatusBadRequest, "rejected by image moderation"
			}
			return c.Status(status).JSON(Envelope[any]{
				Code: errors.ErrOperationFailed, Message: msg,
			})
		}
		return c.JSON(okEnvelope(res))
	}
}
