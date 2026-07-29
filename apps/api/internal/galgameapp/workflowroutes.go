package galgameapp

import (
	galgameHandler "api/internal/platform/galgame/handler"

	"github.com/gofiber/fiber/v3"
)

// ─── The platform workflow face ────────────────────────────────────────────
//
// This file is the successor to the retired readroutes.go. In 09-open-api-phase2
// route-B endgame W5 the /internal galgame READ bridge was split three ways: the
// 29 A/C-bucket public-data reads (galgame list/detail/batch/search-of-taxonomy/
// calendar/stats/check/relations + the four taxonomy families' list/search/by-id
// projections) were retired outright — downstream now consumes the SAME data from
// the frozen public /v1/galgame contract (waves W2-W4) — while the routes here
// are re-chartered as their own designed surface: the platform workflow face.
//
// This is NOT wiki legacy that happened to survive. It is a platform surface in
// the same family as the /internal user-write face (06a, mountInternalWrites) and
// the /internal proposal face (06b, mountInternalPropose): a dual-credential
// (client key + end-user JWT) workflow surface that curated /v1 cannot and should
// not host. It survives the wiki-bridge retirement BY CHARTER, and its face
// metering deliberately stays "galgame_internal" (same as the retired reads) so
// the platform-workflow traffic remains observable on the existing meter — no new
// face token, because these routes were always part of the internal platform, not
// the public data contract.
//
// Per-route charter (one line each):
//   - /mine, /messages/mine ....... JWT personal surface — a signed-in author's
//     own submissions / their review messages.
//   - /drafts, /user/:id/{stats,    community workflow data — the submission /
//     galgames,contributed} ....... contribution graph the kungal wiki UI drives
//     (same family as 06a create/claim/submit).
//   - /search ..................... SearchWithPending: the raw Meilisearch-
//     document passthrough the kungal publish wizard
//     consumes. P1-amended into the B-bucket in W4 —
//     curated /v1 search returns a projected item
//     shape and cannot architecturally reproduce the
//     full Meili doc (+_formatted), so this stays.
//     optionalJWT so an authenticated caller also
//     sees their own pending/declined drafts.
//   - taxonomy {tag,official,engine,series}/:id/revisions(/:rev) .... legacy-shape
//     revision history — future territory of the
//     editing-engine track, deliberately NOT frozen
//     into /v1 (its shape will modernize there).
//   - /galgame/taxonomy/:family/search ..... the SUBMISSION FORM's taxonomy
//     picker (A2-1g). Same query as the /api staff
//     picker, contributor gate: resolving a tag name
//     to the wiki id the submission payload carries
//     is part of filling the form in, so it cannot
//     sit behind taxonomy.edit_any.
//
// The two S2S cron feeds — GET /galgame/messages/feed and /galgame/revisions/
// recent — ALSO survive the retirement but are registered elsewhere (directly in
// mountInternal, not through this registrar), because they are machine-sync
// primitives rather than user-context reads; see devapiface.go.
type workflowRoutes struct {
	galgameH    *galgameHandler.GalgameHandler
	searchH     *galgameHandler.SearchHandler
	submissionH *galgameHandler.SubmissionHandler
	messageH    *galgameHandler.MessageHandler
	taxRevH     *galgameHandler.TaxonomyRevisionHandler
	// contribTaxH serves the submission form's taxonomy picker (A2-1g) — the
	// staff picker's query behind a contributor gate.
	contribTaxH *galgameHandler.ContributorTaxonomyHandler
	// optionalJWT populates user_id when a valid Bearer JWT is present but never
	// blocks; jwtAuth requires one. Same instances the /internal write + proposal
	// faces use.
	optionalJWT fiber.Handler
	jwtAuth     fiber.Handler
}

// register mounts the 16 GET workflow routes onto parent (the /internal group).
// (A2-1e added a 16th surviving /internal read — the ownership-meta batch — but
// it is registered alongside the S2S feeds in mountInternal, not here; see the
// machine-primitive note at the end of this file's doc.)
// It registers ONLY the surviving platform-workflow reads; the 29 retired public-
// data reads are gone (served by /v1), so the /galgame /:gid catch-all no longer
// exists and route ordering is much more relaxed than the old read face's.
//
// One ordering fence is kept on purpose: /mine and /messages/mine are registered
// BEFORE the /user/:id/* param routes and ahead of anything else in the galgame
// group. Nothing binds them today (the /:gid catch-all that used to threaten them
// was retired in W5), but registration order is app-stack-wide in Fiber, so
// keeping the static personal paths first is cheap insurance should a future
// param route be added to this group.
func (r workflowRoutes) register(parent fiber.Router) {
	// ── Galgame workflow (8) ──
	galgame := parent.Group("/galgame")
	// /mine + /messages/mine: JWT-required personal surface, registered first
	// (see the fence note above).
	galgame.Get("/mine", r.jwtAuth, r.submissionH.ListMine)
	galgame.Get("/messages/mine", r.jwtAuth, r.messageH.ListMine)
	// SearchWithPending: raw Meili-document passthrough for the publish wizard
	// (B-bucket per P1 amendment). optionalJWT unlocks the caller's own pending
	// drafts without ever blocking anonymous callers.
	galgame.Get("/search", r.optionalJWT, r.searchH.Galgame)
	galgame.Get("/drafts", r.galgameH.Drafts)
	galgame.Get("/user/:id/stats", r.galgameH.UserStats)
	galgame.Get("/user/:id/galgames", r.galgameH.UserGalgames)
	galgame.Get("/user/:id/contributed", r.galgameH.UserContributedGalgames)
	// Submission-form taxonomy picker (A2-1g): jwtAuth only — a signed-in
	// contributor filling in the form, the same trust level the /internal write
	// and propose faces set. `taxonomy` is a static segment, so it cannot
	// collide with the /user/:id param routes above.
	galgame.Get("/taxonomy/:family/search", r.jwtAuth, r.contribTaxH.Search)

	// ── Taxonomy revisions (8) — legacy-shape history, one pair per family ──
	// Each family group now carries ONLY its revision routes (list + by-rev); the
	// list/search/by-name/galgame-ids reads it used to host retired to /v1, so no
	// bare /:id or /:name sibling remains to collide with "/:id/revisions".
	tag := parent.Group("/tag")
	tag.Get("/:id/revisions", r.taxRevH.TagListRevisions)
	tag.Get("/:id/revisions/:rev", r.taxRevH.TagGetRevision)

	official := parent.Group("/official")
	official.Get("/:id/revisions", r.taxRevH.OfficialListRevisions)
	official.Get("/:id/revisions/:rev", r.taxRevH.OfficialGetRevision)

	engine := parent.Group("/engine")
	engine.Get("/:id/revisions", r.taxRevH.EngineListRevisions)
	engine.Get("/:id/revisions/:rev", r.taxRevH.EngineGetRevision)

	series := parent.Group("/series")
	series.Get("/:id/revisions", r.taxRevH.SeriesListRevisions)
	series.Get("/:id/revisions/:rev", r.taxRevH.SeriesGetRevision)
}
