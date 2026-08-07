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
// service first"). POST /api/v1/catalog/edit/images.
//
// A plain Fiber route rather than a Huma operation: the body is a multipart
// file, which nothing else on this surface carries, and the route still lives
// under /api/v1/catalog so the caller-applied S2S Basic gate covers it exactly
// like the row-set edit ops.
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

// SetupEditImages registers the upload route. Callable with a nil upload for
// spec export — the route is spec-invisible either way (no Huma op).
func SetupEditImages(app *fiber.App, upload EditImageUpload) {
	// The asserted end-user identity, same posture as the edit ops' actor:
	// stamped into the image audit trail (first_uploader_sub) so a byte blob
	// stays attributable to the person who sent it. Asserted, because on this
	// face the S2S client speaks FOR a user it authenticated itself.
	app.Post("/api/v1/catalog/edit/images", editImageHandler(upload, func(c fiber.Ctx) int64 {
		uid, err := strconv.ParseInt(c.FormValue("actor_uid"), 10, 64)
		if err != nil {
			return 0
		}
		return uid
	}))
}

// SetupUserEditImages registers the SAME upload leg on the user-token plane
// (wave 180). Everything about it is the S2S route's — the preset allow-list,
// the limits the image service enforces upstream, the catalog identity the
// bytes land under and the response envelope — except where the uploader comes
// from: there is no actor_uid form field here, because the actor is the
// verified token's `id`, which UserGate has already established.
//
// A plain Fiber route rather than a Huma operation, for the S2S twin's reason:
// the body is multipart. It is therefore spec-invisible on both faces, and
// docs/catalog/01 §4.4 is where it is written down.
func SetupUserEditImages(app *fiber.App, upload EditImageUpload) {
	app.Post(UserPrefix+"/edit/images", editImageHandler(upload, func(c fiber.Ctx) int64 {
		uid, _ := c.Locals("user_id").(uint)
		return int64(uid)
	}))
}

// editImageHandler is the upload leg both faces run. actorUID is the ONLY thing
// that differs between them — asserted in a form field on the S2S face, taken
// from the verified token on the user one — so it is the only thing injected.
func editImageHandler(upload EditImageUpload, actorUID func(fiber.Ctx) int64) fiber.Handler {
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
		if uid := actorUID(c); uid > 0 {
			sub = "kungal:" + strconv.FormatInt(uid, 10)
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
