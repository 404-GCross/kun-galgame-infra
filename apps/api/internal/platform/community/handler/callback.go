package handler

import (
	"encoding/json"
	"log/slog"
	"time"

	"api/internal/platform/community/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// TrustCallback is the plain Fiber handler for the trust enforcement callback
// (step 03 rulings 6/7). It lives OUTSIDE the /api/v1/community S2S Basic-auth
// prefix and authenticates instead with the trust HMAC (X-Trust-Signature /
// X-Trust-Timestamp) over a single shared secret. A bad / stale / missing
// signature → 401; an unsupported action → 200 (so the trust worker does not
// dead-letter it) with an explicit log; a DB error → 500 (retried by trust).
func TrustCallback(secret string, svc *service.CallbackService) fiber.Handler {
	return func(c fiber.Ctx) error {
		body := c.Body()
		if !service.VerifyTrustSignature(secret, c.Get("X-Trust-Timestamp"), c.Get("X-Trust-Signature"), body, time.Now()) {
			return response.Unauthorized(c, errors.ErrAuthUnauthorized)
		}
		var cb service.TrustCallback
		if err := json.Unmarshal(body, &cb); err != nil {
			return response.BadRequest(c, errors.ErrValidationFailed)
		}
		result, err := svc.Handle(c.Context(), cb)
		if err != nil {
			slog.Error("trust callback enforce", "err", err, "disposition_id", cb.DispositionID, "subject_id", cb.SubjectID)
			return response.InternalError(c, errors.ErrInternalServer)
		}
		if result == service.CallbackUnsupported {
			slog.Info("trust callback: action unsupported for community_post, handle manually",
				"action", cb.Action, "disposition_id", cb.DispositionID, "subject_id", cb.SubjectID)
		}
		return response.Success(c, fiber.Map{"ok": true})
	}
}
