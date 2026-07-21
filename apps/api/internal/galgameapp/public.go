package galgameapp

import (
	"api/internal/app"
	"api/internal/platform/devapi"
	galgameHandler "api/internal/platform/galgame/handler"
	galgameSearch "api/internal/platform/galgame/search"
	galgameService "api/internal/platform/galgame/service"

	"github.com/gofiber/fiber/v3"
)

// mountPublic mounts the /v1/galgame public projection group behind the SHARED
// devapi middleware chain (built once by newDevapiFace) and wires per-response
// usage metering under the "galgame" surface. Taxonomy / calendar / reverse-
// lookup endpoints are whitelisted passthroughs of the existing serving
// handlers; the aggregate list / detail / batch / search / changes are the new
// frozen projection. The usage flush lifecycle lives on the shared face, so this
// function starts no ticker and registers no shutdown hook of its own.
func mountPublic(
	a *app.App,
	face devapiFace,
	galgameSvc *galgameService.GalgameService,
	searchSvc *galgameSearch.Service,
	galgameH *galgameHandler.GalgameHandler,
	entityGalgamesH *galgameHandler.EntityGalgamesHandler,
) {
	publicH := galgameHandler.NewPublicHandler(galgameSvc, searchSvc)

	// Force the content_limit gate on passthrough routes that honor the param, so
	// a caller can't pass content_limit=all/nsfw to reach NSFW on the sfw face.
	// ResolveContentLimit is always "sfw" in Phase 1 (no key holds galgame:nsfw).
	// This is a /v1-ONLY gate: the /internal face deliberately does not carry it
	// (content_limit passes through untouched — byte-compat with the /api face).
	sfwGate := func(c fiber.Ctx) error {
		c.Request().URI().QueryArgs().Set("content_limit", devapi.ResolveContentLimit(c, c.Query("content_limit")))
		return c.Next()
	}

	v1 := a.Fiber.Group("/v1/galgame",
		face.mw.ResolveCredential,
		face.recordUsage("galgame"),
		face.mw.RateLimit,
		face.mw.Quota,
		devapi.RequireScope(devapi.ScopeGalgameRead),
	)

	// Static paths first; the /:id detail catch-all is registered LAST so it never
	// binds "search" / "batch" / "changes" / "calendar" / "tags" / … .
	v1.Get("/", publicH.List)
	v1.Get("/search", publicH.Search)
	v1.Get("/batch", publicH.Batch)
	v1.Get("/changes", publicH.Changes)
	// Calendar passthrough (already ETag/cache'd) — sfw-forced.
	v1.Get("/calendar", sfwGate, galgameH.Calendar)
	v1.Get("/calendar/pending", sfwGate, galgameH.CalendarPending)
	v1.Get("/calendar/tba", sfwGate, galgameH.CalendarTBA)
	// Taxonomy bare lists are deliberately NOT mounted (reviewer ruling, step 02):
	// they would serve the internal recursive model shape outside the published
	// spec, and anything reachable on the frozen /v1 face becomes de-facto
	// contract (the same reasoning that made search a projection instead of a
	// raw passthrough). Curated taxonomy projections come as a follow-up.
	// Entity → galgames reverse-lookups (carry galgames → sfw-forced).
	v1.Get("/tags/:id/galgames", sfwGate, entityGalgamesH.TagGalgames)
	v1.Get("/officials/:id/galgames", sfwGate, entityGalgamesH.OfficialGalgames)
	// Detail catch-all — MUST be last.
	v1.Get("/:id", publicH.Detail)
}
