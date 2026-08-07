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

// edit_images.go — the byte-upload leg of the edit face (the N2/N3 delivery
// editspec/work_media.go promises: "a client obtains a hash from the image
// service first").
//
// A plain Fiber route rather than a Huma operation: the body is a multipart
// file, which nothing else on this surface carries. It is therefore
// spec-invisible, and docs/catalog/01 §4.4 is where it is written down.
//
// Bytes land under the CATALOG image-service identity (site_key "catalog") —
// the same scope the daily catalog refping sweep keeps alive — so a hash the
// editor then attaches to covers/screenshots is pinged by the existing cron
// with no new plumbing. The galgame_wiki image key is never touched (03 §2).

// editImagePresets is the closed set of wire presets this face accepts, each
// mapped to the preset actually sent upstream. Callers still speak the
// galgame_* names the forum FE has always sent, but the bytes are stored under
// the catalog site's own presets (same 460x259 mini variant, superset MIME) —
// one preset per asset kind across editor uploads and the aggregation-job
// backfills, and the catalog image client's allow-list already contains them.
// The image service gates presets per client again upstream; the closed set
// here keeps the route from becoming an open proxy into arbitrary presets.
var editImagePresets = map[string]string{
	"galgame_banner":     "catalog_cover",
	"galgame_screenshot": "catalog_screenshot",
}

// EditImageUpload forwards one file to the image service under the catalog
// identity. Nil means the image client is not configured (upload disabled).
type EditImageUpload func(ctx context.Context, r io.Reader, filename, preset, uploaderSub string) (*imageclient.UploadResult, error)

// SetupUserEditImages registers the upload leg on the user-token plane. It is
// the only face that uploads: the S2S twin took its uploader from an actor_uid
// form field, which wave 181 retired along with every other asserted human
// identity. Callable with a nil upload — the leg then answers 503.
func SetupUserEditImages(app *fiber.App, upload EditImageUpload) {
	app.Post(UserPrefix+"/edit/images", editImageHandler(upload))
}

// editImageHandler forwards one multipart file. The uploader stamped into the
// image audit trail (first_uploader_sub) is the verified token's `id`, which
// UserGate has already established — so a byte blob stays attributable to the
// person who actually sent it.
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
