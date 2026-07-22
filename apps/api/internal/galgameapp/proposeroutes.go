package galgameapp

import (
	"api/internal/app"
	"api/internal/platform/devapi"

	"github.com/gofiber/fiber/v3"
)

// mountInternalPropose mounts the /internal/edit/* platform proposal face
// (09-open-api-phase2 06b): the six user-proposal operations behind the shared
// devapi chain, paired with the end-user JWT (dual-credential: X-API-Key =
// client identity, Authorization: Bearer = the user token).
//
// The chain is the /internal write chain's propose variant — same
// ResolveCredential → RateLimit → Quota shape — metered under its own face
// (galgame_internal_propose, so proposal traffic is independently observable),
// gated on ScopeGalgamePropose, and terminating in the SAME jwtAuth Mount
// builds. There is no sfwGate (proposals carry no content_limit).
//
// CRITICAL ordering (identical discipline to mountInternalWrites): this MUST be
// called BEFORE mountInternal. mountInternal installs Group("/internal",
// readChain...), a Fiber prefix Use that blankets every /internal/* route
// registered after it. Attaching the propose chain PER ROUTE (never as a group
// Use) and registering this face first keeps the read chain from blanketing it,
// while the route-level handlers blanket nothing themselves. The faces never
// collide on path (this one owns /internal/edit/*, disjoint from the reads'
// /internal/galgame|tag|official|engine|series and the writes' /internal/galgame).
func mountInternalPropose(a *app.App, face devapiFace, ph *proposeHandler, jwtAuth fiber.Handler) {
	chain := []fiber.Handler{
		face.mw.ResolveCredential,
		face.recordUsage("galgame_internal_propose"),
		devapi.RequireTier(devapi.TierInternal),
		face.mw.RateLimit,
		face.mw.Quota,
		devapi.RequireScope(devapi.ScopeGalgamePropose),
		jwtAuth,
	}
	// PLAIN group (no group middleware) — the chain rides each route so it never
	// becomes a blanket /internal Use.
	registerPropose(a.Fiber.Group("/internal"), chain, ph)
}

// registerPropose mounts the six routes onto parent (the plain /internal group),
// prepending `chain` to every route's handler slice. Static/multi-segment paths
// are registered ahead of the /:id parameter segment so ":id" never binds
// "schema"/"snapshot"; registration order is app-stack-wide in Fiber.
func registerPropose(parent fiber.Router, chain []fiber.Handler, ph *proposeHandler) {
	edit := parent.Group("/edit")

	// add mounts one route with a FRESH handler slice (chain + the route's own
	// handler) so no two routes alias the same backing array (Fiber v3's Add
	// takes (path, handler any, handlers ...any)).
	add := func(method, path string, h fiber.Handler) {
		all := make([]any, 0, len(chain)+1)
		for _, hd := range chain {
			all = append(all, hd)
		}
		all = append(all, h)
		edit.Add([]string{method}, path, all[0], all[1:]...)
	}

	// ── Static / multi-segment first ──
	add(fiber.MethodGet, "/snapshot", ph.snapshot)
	add(fiber.MethodGet, "/schema/:entity_type", ph.schema)
	add(fiber.MethodPost, "/proposals", ph.create)
	add(fiber.MethodGet, "/proposals", ph.mine)
	// ── /:id parameter segment ──
	add(fiber.MethodPost, "/proposals/:id/withdraw", ph.withdraw)
	add(fiber.MethodGet, "/proposals/:id", ph.get)
}
