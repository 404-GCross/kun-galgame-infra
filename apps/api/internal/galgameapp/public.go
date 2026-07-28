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
	publicTaxSvc *galgameService.PublicTaxonomyService,
	optionalJWT fiber.Handler,
) {
	publicH := galgameHandler.NewPublicHandler(galgameSvc, searchSvc)
	// Curated taxonomy four-family public face (W1b): tags / officials / engines /
	// series by-id projections over the same devapi chain + Meilisearch service.
	taxH := galgameHandler.NewPublicTaxonomyHandler(publicTaxSvc, searchSvc)

	// Force the content_limit gate on passthrough routes that honor the param, so
	// a caller can't pass content_limit=all/nsfw to reach NSFW on the sfw face.
	// ResolveContentLimit is always "sfw" in Phase 1 (no key holds galgame:nsfw).
	// This is a /v1-ONLY gate: the /internal face deliberately does not carry it
	// (content_limit passes through untouched — byte-compat with the /api face).
	sfwGate := func(c fiber.Ctx) error {
		c.Request().URI().QueryArgs().Set("content_limit", devapi.ResolveContentLimit(c, c.Query("content_limit")))
		return c.Next()
	}

	// doc 106 绞杀式弃用: the /v1/galgame public face retires on 2026-10-31
	// (90-day window, canonical data lives on /v1/catalog). RFC 9745
	// Deprecation + RFC 8594 Sunset + a successor Link on every response.
	deprecationHeaders := func(c fiber.Ctx) error {
		c.Set("Deprecation", "@1785196800")
		c.Set("Sunset", "Sat, 31 Oct 2026 00:00:00 GMT")
		c.Set("Link", `<https://api.nextmoe.dev/v1/catalog>; rel="successor-version"`)
		return c.Next()
	}
	v1 := a.Fiber.Group("/v1/galgame",
		deprecationHeaders,
		face.mw.ResolveCredential,
		face.recordUsage("galgame"),
		face.mw.RateLimit,
		face.mw.Quota,
		devapi.RequireScope(devapi.ScopeGalgameRead),
	)

	// Static paths first; the /:id detail catch-all is registered LAST so it never
	// binds "search" / "batch" / "changes" / "stats" / "lookup" / "calendar" / … .
	v1.Get("/", publicH.List)
	// search takes the OPTIONAL end-user JWT (dual-credential): the key rides in
	// X-API-Key, so Authorization is free for the Bearer JWT that unlocks
	// include_pending (P7). optionalJWT never blocks — anonymous callers just get
	// no pending[] key.
	v1.Get("/search", optionalJWT, publicH.Search)
	v1.Get("/batch", publicH.Batch)
	v1.Get("/changes", publicH.Changes)
	// Cross-source stats (W1a) + vndb_id existence lookup (W1a). Static paths, so
	// they are registered ahead of the /:id catch-all.
	v1.Get("/stats", publicH.Stats)
	v1.Get("/lookup", publicH.Lookup)
	// Calendar passthrough (already ETag/cache'd) — sfw-forced.
	v1.Get("/calendar", sfwGate, galgameH.Calendar)
	v1.Get("/calendar/pending", sfwGate, galgameH.CalendarPending)
	v1.Get("/calendar/tba", sfwGate, galgameH.CalendarTBA)
	// Taxonomy bare lists are deliberately NOT mounted (reviewer ruling, step 02):
	// they would serve the internal recursive model shape outside the published
	// spec, and anything reachable on the frozen /v1 face becomes de-facto
	// contract (the same reasoning that made search a projection instead of a
	// raw passthrough). Curated taxonomy projections come as a follow-up.
	// ── Taxonomy four-family public faces (W1b) ──
	// Static segments (search / multi) are registered BEFORE each family's /:id
	// catch-all so they never bind to the id param; the entity→galgames reverse-
	// lookups (sfw-forced, they carry galgame previews) keep their existing wiring.
	// Tags (list / search / multi / {id} / {id}/galgame-ids + existing reverse-lookup).
	v1.Get("/tags", taxH.TagList)
	v1.Get("/tags/search", taxH.TagSearch)
	v1.Get("/tags/multi", taxH.TagMulti)
	v1.Get("/tags/:id/galgames", sfwGate, entityGalgamesH.TagGalgames)
	v1.Get("/tags/:id/galgame-ids", taxH.TagGalgameIDs)
	v1.Get("/tags/:id", taxH.Tag)
	// Officials (list / search / {id} / {id}/galgame-ids + existing reverse-lookup).
	v1.Get("/officials", taxH.OfficialList)
	v1.Get("/officials/search", taxH.OfficialSearch)
	v1.Get("/officials/:id/galgames", sfwGate, entityGalgamesH.OfficialGalgames)
	v1.Get("/officials/:id/galgame-ids", taxH.OfficialGalgameIDs)
	v1.Get("/officials/:id", taxH.Official)
	// Engines (list / {id} / {id}/galgame-ids).
	v1.Get("/engines", taxH.EngineList)
	v1.Get("/engines/:id/galgame-ids", taxH.EngineGalgameIDs)
	v1.Get("/engines/:id", taxH.Engine)
	// Series (list / {id}).
	v1.Get("/series", taxH.SeriesList)
	v1.Get("/series/:id", taxH.Series)
	// Detail catch-all — MUST be last.
	v1.Get("/:id", publicH.Detail)
}
