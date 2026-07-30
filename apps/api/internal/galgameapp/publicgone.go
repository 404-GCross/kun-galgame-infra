package galgameapp

import (
	"api/internal/app"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// The /v1/galgame public projection is RETIRED (wave 146 Lane A, 2026-07-30).
//
// doc 106 opened a 90-day strangler window with a 2026-10-31 Sunset; the window
// was cut short by user order once the traffic evidence was unambiguous — a 12h
// sample counted 146,236 hits on the canonical /v1/catalog face against 1 on
// /v1/galgame (a bare-path scanner that 401'd). The 26 public operations that
// used to be registered here (list / detail / batch / search / changes / stats /
// lookup / calendar ×3 / entity reverse-lookups ×2 / taxonomy by-id ×14) are
// gone; nothing on this prefix is served any more.
//
// SURVIVING faces are deliberately untouched by this file: the /api staff face,
// the devapi-gated /internal platform-workflow + write + propose faces, and the
// whole canonical /v1/catalog face.
//
// 410 rather than 404 is the point: it separates "this face was decommissioned,
// here is the successor" from "you typed the path wrong", so a stale third-party
// client gets a diagnosis instead of a mystery. The RFC 9745 Deprecation and RFC
// 8594 Sunset headers the live face carried retired WITH it — they announce a
// FUTURE retirement and would be a lie now; the successor Link survives because
// it is still true.
const (
	// retiredSuccessorLink points at the canonical face that replaced this one.
	retiredSuccessorLink = `<https://api.nextmoe.dev/v1/catalog>; rel="successor-version"`
	// retiredPublicMessage is English because this face's audience is
	// third-party API consumers (same convention as the devapi middleware's
	// error messages), and it names the successor plus its documentation.
	retiredPublicMessage = "the /v1/galgame face was retired on 2026-07-30; " +
		"use the canonical /v1/catalog face instead — https://developer.nextmoe.dev/docs/catalog"
)

// mountRetiredPublic parks the retired /v1/galgame prefix on a 410 Gone catch-all.
//
// It takes no credential, no scope and no rate limiter: a decommissioned face
// must answer the same way to everyone, and making a caller authenticate only to
// learn the endpoint is gone would be a worse diagnosis than the 401 the old
// devapi chain produced. Both the bare prefix and every sub-path are registered,
// for every method, so no verb slips through to Fiber's plain 404.
func mountRetiredPublic(a *app.App) {
	gone := func(c fiber.Ctx) error {
		c.Set("Link", retiredSuccessorLink)
		return response.Error(c, fiber.StatusGone, errors.ErrGone, retiredPublicMessage)
	}
	a.Fiber.All("/v1/galgame", gone)
	a.Fiber.All("/v1/galgame/*", gone)
}
