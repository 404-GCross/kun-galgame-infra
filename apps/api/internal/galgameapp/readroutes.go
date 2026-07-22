package galgameapp

import (
	galgameHandler "api/internal/platform/galgame/handler"

	"github.com/gofiber/fiber/v3"
)

// readRoutes carries the handler + JWT-middleware references for the galgame
// read surface. Since 09-open-api-phase2 wave 05 A2 retired the legacy /api read
// face, its register method backs the devapi-gated /internal rich read face
// only. It is assembled once in Mount and its register method is called on that
// face's parent group. (The handler/service instances are the same ones the
// /internal write face and the /api staff face use — no handler logic is
// duplicated.)
type readRoutes struct {
	galgameH        *galgameHandler.GalgameHandler
	searchH         *galgameHandler.SearchHandler
	entityGalgamesH *galgameHandler.EntityGalgamesHandler
	linkH           *galgameHandler.LinkHandler
	contributorH    *galgameHandler.ContributorHandler
	submissionH     *galgameHandler.SubmissionHandler
	messageH        *galgameHandler.MessageHandler
	tagH            *galgameHandler.TagHandler
	officialH       *galgameHandler.OfficialHandler
	engineH         *galgameHandler.EngineHandler
	seriesH         *galgameHandler.SeriesHandler
	taxRevH         *galgameHandler.TaxonomyRevisionHandler
	// optionalJWT populates user_id when a valid Bearer JWT is present but never
	// blocks; jwtAuth requires one. Same instances the /internal write face uses.
	optionalJWT fiber.Handler
	jwtAuth     fiber.Handler
}

// register mounts the 44 GET read routes onto parent (the /internal group since
// wave 05 A2; historically also the /api group), creating the five entity
// sub-groups. It registers ONLY read routes and preserves the ordering the /api
// read face established:
//   - static / multi-segment paths (calendar, stats, user/:id/*, officials|tags
//     reverse-lookups) and /mine + /messages/mine are registered BEFORE the
//     galgame /:gid catch-all, so ":gid" never binds "mine"/"calendar"/… .
//   - tag/official/engine /search is registered before /:name (else /:name binds
//     "search"); series /search before /:id.
//
// Keeping the registration in this one helper means the read-route order stays
// correct on the face it backs. Write / admin / S2S-feed / catalog-proxy routes
// are NOT part of the read face — the /api caller registers those separately.
func (r readRoutes) register(parent fiber.Router) {
	// ── Galgame (21) ──
	galgame := parent.Group("/galgame")
	galgame.Get("/", r.galgameH.List)
	// search & batch take an optional Bearer JWT so an authenticated caller also
	// sees their own pending/declined drafts; anonymous callers get status=0 only.
	galgame.Get("/search", r.optionalJWT, r.searchH.Galgame)
	galgame.Get("/batch", r.optionalJWT, r.galgameH.BatchGet)
	galgame.Get("/drafts", r.galgameH.Drafts)
	galgame.Get("/check", r.galgameH.CheckVNDB)
	galgame.Get("/user/:id/stats", r.galgameH.UserStats)
	galgame.Get("/user/:id/galgames", r.galgameH.UserGalgames)
	galgame.Get("/user/:id/contributed", r.galgameH.UserContributedGalgames)
	galgame.Get("/calendar", r.galgameH.Calendar)
	galgame.Get("/calendar/pending", r.galgameH.CalendarPending)
	galgame.Get("/calendar/tba", r.galgameH.CalendarTBA)
	galgame.Get("/stats", r.galgameH.Stats)
	galgame.Get("/officials/:id/galgames", r.entityGalgamesH.OfficialGalgames)
	galgame.Get("/tags/:id/galgames", r.entityGalgamesH.TagGalgames)
	// /mine and /messages/mine require a real end-user JWT (jwtAuth). Both are
	// static multi-segment paths → before the /:gid catch-all.
	galgame.Get("/mine", r.jwtAuth, r.submissionH.ListMine)
	galgame.Get("/messages/mine", r.jwtAuth, r.messageH.ListMine)
	galgame.Get("/:gid", r.optionalJWT, r.galgameH.Get)
	galgame.Get("/:gid/links", r.linkH.ListLinks)
	galgame.Get("/:gid/aliases", r.linkH.ListAliases)
	galgame.Get("/:gid/contributors", r.contributorH.List)
	galgame.Get("/:gid/scores", r.galgameH.Scores)

	// ── Tag (7) ──
	tag := parent.Group("/tag")
	tag.Get("/", r.tagH.List)
	tag.Get("/search", r.searchH.Tag) // Meilisearch-backed
	tag.Get("/multi", r.tagH.Multi)
	tag.Get("/:name", r.tagH.GetByName)
	tag.Get("/:id/galgame-ids", r.tagH.GalgameIDs)
	tag.Get("/:id/revisions", r.taxRevH.TagListRevisions)
	tag.Get("/:id/revisions/:rev", r.taxRevH.TagGetRevision)

	// ── Official (6) ── (no /multi)
	official := parent.Group("/official")
	official.Get("/", r.officialH.List)
	official.Get("/search", r.searchH.Official) // Meilisearch-backed
	official.Get("/:name", r.officialH.GetByName)
	official.Get("/:id/galgame-ids", r.officialH.GalgameIDs)
	official.Get("/:id/revisions", r.taxRevH.OfficialListRevisions)
	official.Get("/:id/revisions/:rev", r.taxRevH.OfficialGetRevision)

	// ── Engine (5) ── (no /search, no /multi)
	engine := parent.Group("/engine")
	engine.Get("/", r.engineH.List)
	engine.Get("/:name", r.engineH.GetByName)
	engine.Get("/:id/galgame-ids", r.engineH.GalgameIDs)
	engine.Get("/:id/revisions", r.taxRevH.EngineListRevisions)
	engine.Get("/:id/revisions/:rev", r.taxRevH.EngineGetRevision)

	// ── Series (5) ── (/:id, not /:name; no /galgame-ids, no /multi)
	series := parent.Group("/series")
	series.Get("/", r.seriesH.List)
	series.Get("/search", r.seriesH.Search)
	series.Get("/:id", r.seriesH.Get)
	series.Get("/:id/revisions", r.taxRevH.SeriesListRevisions)
	series.Get("/:id/revisions/:rev", r.taxRevH.SeriesGetRevision)
}
