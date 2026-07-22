package galgameapp

import (
	"api/internal/middleware"
	galgameHandler "api/internal/platform/galgame/handler"
	galgamePerm "api/internal/platform/galgame/perm"

	"github.com/gofiber/fiber/v3"
)

// writeRoutes carries the handler references for the galgame user-write surface
// — the 12 jwtAuth-gated write routes the legacy /api face registers under its
// Bearer fence (galgameAuth group). Since 09-open-api-phase2 wave 06a W1 this
// same handler set is ALSO mounted on the devapi-gated /internal write face (see
// mountInternalWrites); the two faces share the SAME handler instances Mount
// builds — no handler logic is duplicated. The legacy /api registrations are
// untouched here (they retire in W3).
type writeRoutes struct {
	galgameH     *galgameHandler.GalgameHandler
	linkH        *galgameHandler.LinkHandler
	contributorH *galgameHandler.ContributorHandler
	submissionH  *galgameHandler.SubmissionHandler
}

// register mounts the 12 user-write routes onto parent (the plain /internal
// group — NO group middleware), prepending `chain` (the devapi write chain
// terminating in jwtAuth) to every route's handler slice.
//
// The chain is attached PER ROUTE rather than as group middleware on purpose.
// The /internal read face already installs its own group-level
// devapi-read chain via Group("/internal", readChain...), which in Fiber is a
// prefix Use that blankets every /internal/* route registered AFTER it (the same
// empty-prefix fence documented in mount.go). Group-level write middleware at
// the same /internal prefix would therefore either be blanketed by the read
// chain (wrong scope) or blanket the reads itself. Route-level handlers are
// bound to their exact route and blanket nothing — so mountInternalWrites
// registers this face BEFORE the read face's Group Use, and the two chains stay
// disjoint (the reads are GET-only, the writes POST/PUT/PATCH/DELETE, so they
// never collide on method either).
//
// Registration order mirrors the /api Bearer group: the static segments
// (/, /image, /submit) are registered before the /:gid parameter segment so
// ":gid" never binds "submit"/"image"; registration order is app-stack-wide in
// Fiber.
func (w writeRoutes) register(parent fiber.Router, chain []fiber.Handler) {
	galgame := parent.Group("/galgame")

	// add mounts one route with a FRESH handler slice (chain + the route's own
	// handlers) so no two routes alias the same backing array. Fiber v3's route
	// methods take (path, handler any, handlers ...any), so the composed chain is
	// spread through Add as []any.
	add := func(method, path string, h ...fiber.Handler) {
		all := make([]any, 0, len(chain)+len(h))
		for _, hd := range chain {
			all = append(all, hd)
		}
		for _, hd := range h {
			all = append(all, hd)
		}
		galgame.Add([]string{method}, path, all[0], all[1:]...)
	}

	// ── Static segments first ──
	// POST / is the admin direct-publish bypass (creates status=0 immediately);
	// locked to galgame.create so non-staff cannot skip the submission queue.
	add(fiber.MethodPost, "/", middleware.RequirePermission(galgamePerm.Resolver, galgamePerm.Create), w.galgameH.Create)
	// Canonical galgame image upload (cover + screenshot) under the wiki image
	// client (site=galgame_wiki). The storage identity is unchanged — only the
	// auth entry point moves to the devapi write chain.
	add(fiber.MethodPost, "/image", w.galgameH.UploadImage)
	// User submission flow — Submit creates a status=3 draft awaiting review.
	add(fiber.MethodPost, "/submit", w.submissionH.Submit)

	// ── /:gid parameter segment ──
	add(fiber.MethodPut, "/:gid", w.galgameH.Update)
	add(fiber.MethodPatch, "/:gid", w.submissionH.PatchDraft)
	add(fiber.MethodDelete, "/:gid", w.submissionH.DeleteDraft)
	add(fiber.MethodPost, "/:gid/claim", w.submissionH.Claim)
	add(fiber.MethodPost, "/:gid/links", w.linkH.CreateLink)
	add(fiber.MethodDelete, "/:gid/links", w.linkH.DeleteLink)
	add(fiber.MethodPost, "/:gid/aliases", w.linkH.CreateAlias)
	add(fiber.MethodDelete, "/:gid/aliases", w.linkH.DeleteAlias)
	add(fiber.MethodDelete, "/:gid/contributors/:id", w.contributorH.Delete)
}
