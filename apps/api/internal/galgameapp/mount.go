// Package galgameapp is the galgame service's HTTP-surface assembly, extracted
// so it can be mounted into more than one binary. Before the wiki-retirement W2
// merge, the galgame routes lived inline in the standalone galgame service
// binary (:9280, retired at W3/W5); since W2, cmd/catalog calls Mount so the
// merged catalog process serves the galgame surface from the same kun_catalog
// database — and since W5 it is the surface's only host.
//
// The extraction is behavior-preserving: Mount registers exactly the routes,
// middleware, and lifecycle hooks that the old standalone binary's setupRoutes +
// setupPublicGalgame did. The one thing Mount does NOT own is the shared global
// middleware (RequestID / Logger / CORS) and the root /healthz probe — those are
// per-process concerns the caller registers once, so co-hosting galgame inside
// cmd/catalog does not double-register them.
package galgameapp

import (
	"context"
	"log/slog"

	"api/internal/app"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	catalogPerm "api/internal/platform/catalog/perm"
	galgameHandler "api/internal/platform/galgame/handler"
	galgamePerm "api/internal/platform/galgame/perm"
	galgameRepo "api/internal/platform/galgame/repository"
	galgameSearch "api/internal/platform/galgame/search"
	galgameService "api/internal/platform/galgame/service"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/catalogclient"
	"api/pkg/config"
	"api/pkg/imageclient"
	"api/pkg/oidctoken"

	"gorm.io/gorm"
)

// Deps are the pre-built infrastructure handles the galgame surface mounts onto.
// The caller (cmd/catalog) owns their lifecycle; Mount only reads from them.
type Deps struct {
	// OAuthDB is the kun_galgame_infra connection: read-only user lookups, the
	// OAuth client registry (Basic-Auth cron callers), and the developer-platform
	// tables the /v1 devapi chain reads.
	OAuthDB *gorm.DB
	// GalgameDB is the galgame content database (kun_galgame_wiki pre-W1,
	// kun_catalog after) — read-write.
	GalgameDB *gorm.DB
	// Search is the shared Meilisearch client. Mount self-heals the galgame index
	// settings and builds the galgame indexer / write-through hook / search
	// service from it.
	Search *searchInfra.Client
}

// Mount wires the galgame domain (repositories, services, handlers) and registers
// its full HTTP surface — the internal /api/galgame|tag|official|engine|series
// read+write routes, the admin routes, the S2S cron feeds, the catalog data
// browser proxy, and the NextMoe /v1/galgame public projection — onto a.Fiber.
//
// Global middleware and /healthz are the caller's responsibility (see the package
// doc); Mount assumes CORS + request-id + logging are already installed.
func Mount(a *app.App, cfg *config.Config, deps Deps) {
	// Meilisearch index-settings self-heal for the galgame indexes. Non-fatal:
	// search endpoints degrade but DB-backed routes still work; cmd/reindex-search
	// is the recovery path. Travels with the galgame subsystem so every process
	// that mounts galgame (galgame + merged catalog) heals its own indexes.
	if err := galgameSearch.EnsureIndexes(deps.Search); err != nil {
		slog.Warn("EnsureIndexes failed — search endpoints may not work until fixed", "error", err)
	} else {
		galgameSearch.WarnIfIndexesEmpty(deps.Search)
	}

	oauthDB := deps.OAuthDB // kun_galgame_infra — read-only for user info
	wiki := deps.GalgameDB  // galgame content DB — read-write
	searchClient := deps.Search

	// Repositories
	galgameRepository := galgameRepo.NewGalgameRepository(wiki)
	userReadRepo := galgameRepo.NewUserReadonlyRepository(oauthDB)
	revisionRepo := galgameRepo.NewRevisionRepository(wiki)
	prRepo := galgameRepo.NewPRRepository(wiki)
	tagRepo := galgameRepo.NewTagRepository(wiki)
	officialRepo := galgameRepo.NewOfficialRepository(wiki)
	engineRepo := galgameRepo.NewEngineRepository(wiki)
	seriesRepo := galgameRepo.NewSeriesRepository(wiki)
	adminRepo := galgameRepo.NewAdminRepository(wiki)
	messageRepo := galgameRepo.NewMessageRepository(wiki)
	taxRevRepo := galgameRepo.NewTaxonomyRevisionRepository(wiki)
	// OAuth client repo lives on the OAuth DB (read-only from galgame service);
	// used to authenticate Basic-Auth cron callers on the message feed.
	oauthClientRepo := siteRepo.NewOAuthClientRepository(oauthDB)

	// Services
	galgameSvc := galgameService.NewGalgameService(galgameRepository, revisionRepo, prRepo, userReadRepo).
		WithCDNBase(cfg.ImageService.CDNBase)
	submissionSvc := galgameService.NewSubmissionService(galgameRepository, messageRepo)
	messageSvc := galgameService.NewMessageService(messageRepo, galgameRepository, userReadRepo)
	adminSvc := galgameService.NewAdminService(galgameRepository, messageRepo)
	// TaxonomyService — orchestrates tag/official/engine/series CRUD via
	// the polymorphic taxonomy_revision audit. Every mutating handler
	// path below routes through it.
	taxSvc := galgameService.NewTaxonomyService(tagRepo, officialRepo, engineRepo, seriesRepo, taxRevRepo, galgameRepository)

	// Meilisearch: indexer + write-through hook + search service
	indexer := galgameSearch.NewIndexer(searchClient)
	searchHook := galgameSearch.NewHook(wiki, indexer)
	searchSvc := galgameSearch.NewService(searchClient)

	// Image client (singleton, optional). Used by Galgame Create/Update +
	// PR submit when caller sends multipart with a banner file.
	// Nil if KUN_IMAGE_CLIENT_ID/SECRET unset → multipart with file 400s
	// with a clear error; JSON-only callers unaffected.
	var imgCli *imageclient.Client
	if cfg.ImageClient.ClientID != "" && cfg.ImageClient.ClientSecret != "" {
		imgCli = imageclient.New(imageclient.Config{
			BaseURL:      cfg.ImageClient.BaseURL,
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     cfg.ImageClient.ClientID,
			ClientSecret: cfg.ImageClient.ClientSecret,
		})
		slog.Info("image client configured", "base_url", cfg.ImageClient.BaseURL)

		// Wire the existence probe into the galgame service: Revert uses
		// it to detect snapshots that point at TTL-deleted image hashes.
		// Implementation reuses ReferencePing — which both probes
		// existence (returns NotFound) AND refreshes the TTL for the
		// hashes that *do* exist, so revert is also a free "ref-touch"
		// for everything in the reverted snapshot.
		galgameSvc.WithImageProbe(func(ctx context.Context, hashes []string) ([]string, error) {
			res, err := imgCli.ReferencePing(ctx, hashes)
			if err != nil {
				return nil, err
			}
			return res.NotFound, nil
		})

		// Wire the image-metadata lookup so the read paths enrich covers /
		// screenshots / banners with dimensions + thumbhash (blur-up
		// placeholder + correct aspect ratio, no layout shift). Results are
		// cached in the service (immutable per content-addressed hash).
		galgameSvc.WithImageMeta(func(ctx context.Context, hashes []string) (map[string]galgameService.ImageMeta, error) {
			raw, err := imgCli.MetaBatch(ctx, hashes)
			if err != nil {
				return nil, err
			}
			out := make(map[string]galgameService.ImageMeta, len(raw))
			for h, m := range raw {
				out[h] = galgameService.ImageMeta{Width: m.Width, Height: m.Height, Thumbhash: m.Thumbhash}
			}
			return out, nil
		})
	} else {
		slog.Warn("image client not configured; multipart banner uploads in galgame Create/Update/PR will be rejected; Revert will skip image-existence probe")
	}

	// Catalog client for the internal data browser proxy (step 19). Nil when
	// unconfigured → the staff browser routes soft-503.
	catalogCli := catalogclient.New(catalogclient.Config{
		BaseURL:      cfg.CatalogClient.BaseURL,
		ClientID:     cfg.CatalogClient.ClientID,
		ClientSecret: cfg.CatalogClient.ClientSecret,
	})
	if catalogCli != nil {
		slog.Info("catalog client configured", "base_url", cfg.CatalogClient.BaseURL)
	}

	// Handlers
	galgameH := galgameHandler.NewGalgameHandler(galgameSvc, searchHook, imgCli)
	revisionH := galgameHandler.NewRevisionHandler(galgameSvc, imgCli)
	linkH := galgameHandler.NewLinkHandler(galgameSvc, galgameRepository)
	contributorH := galgameHandler.NewContributorHandler(galgameRepository, userReadRepo)
	tagH := galgameHandler.NewTagHandler(tagRepo, taxSvc, searchHook)
	officialH := galgameHandler.NewOfficialHandler(officialRepo, taxSvc, searchHook)
	// Entity reverse-lookups (step 20): official/tag → self-description + galgame
	// briefs, in the /galgame read namespace (part of read-openapi).
	entityGalgamesH := galgameHandler.NewEntityGalgamesHandler(officialRepo, tagRepo, galgameSvc)
	engineH := galgameHandler.NewEngineHandler(engineRepo, taxSvc)
	seriesH := galgameHandler.NewSeriesHandler(seriesRepo, taxSvc)
	// One handler covers ListRevisions / GetRevision / Revert for all
	// four taxonomy entities — the entity discriminator is baked into
	// the route wrapper methods (TagListRevisions / OfficialRevert / …).
	taxRevH := galgameHandler.NewTaxonomyRevisionHandler(taxSvc)
	adminH := galgameHandler.NewAdminHandler(adminRepo, adminSvc, searchHook)
	submissionH := galgameHandler.NewSubmissionHandler(submissionSvc, searchHook, imgCli)
	messageH := galgameHandler.NewMessageHandler(messageSvc)
	searchH := galgameHandler.NewSearchHandler(searchSvc)

	// JWT auth middleware — backed by an accept-both verifier (ES256/RS256 via
	// the OP's JWKS + legacy HS256). HS256-only when KUN_OIDC_JWKS_URL is unset.
	// See docs/auth/03-oidc-standardization-design.md §10 Phase 1.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	jwtAuth := middleware.JWTAuth(tokenVerifier)
	// OptionalJWT — populates user_id when a valid Bearer token is present,
	// but never blocks the request. Used on /galgame/batch and /galgame/search
	// so anonymous callers still get status=0-only results while authenticated
	// ones additionally see their own pending/declined drafts.
	optionalJWT := middleware.OptionalJWT(tokenVerifier)

	// API routes
	api := a.Fiber.Group("/api")

	// ── Galgame ──
	galgame := api.Group("/galgame")

	// Public GET routes (must be registered before auth group)
	galgame.Get("/", galgameH.List)
	// search & batch accept an optional Bearer JWT so authenticated callers
	// can also see their own pending/declined drafts (include_pending=true
	// for search; automatic for batch).
	galgame.Get("/search", optionalJWT, searchH.Galgame)
	galgame.Get("/batch", optionalJWT, galgameH.BatchGet)
	galgame.Get("/drafts", galgameH.Drafts)
	galgame.Get("/check", galgameH.CheckVNDB)
	galgame.Get("/user/:id/stats", galgameH.UserStats)
	galgame.Get("/user/:id/galgames", galgameH.UserGalgames)
	galgame.Get("/user/:id/contributed", galgameH.UserContributedGalgames)
	// Release calendar (static paths → before the /:gid catch-all). Public,
	// content_limit via query param so the URL fully keys the cache.
	galgame.Get("/calendar", galgameH.Calendar)
	galgame.Get("/calendar/pending", galgameH.CalendarPending)
	galgame.Get("/calendar/tba", galgameH.CalendarTBA)
	// Cross-source stats overview (step 34). Static path → before the /:gid
	// catch-all. Public; ETag-cached like the calendar.
	galgame.Get("/stats", galgameH.Stats)
	// Entity reverse-lookups (step 20). Static multi-segment paths, registered
	// before the /:gid catch-all so ":gid" never binds "officials"/"tags".
	// Public (SFW-gated via content_limit); powers downstream entity pages.
	galgame.Get("/officials/:id/galgames", entityGalgamesH.OfficialGalgames)
	galgame.Get("/tags/:id/galgames", entityGalgamesH.TagGalgames)
	// GET /mine MUST be registered before the /:gid catch-all: both are
	// GET and Fiber matches by registration order, so a /:gid registered
	// first binds :gid="mine" and the handler ParseInt-fails with
	// {"code":2,"无效的 ID"}. It needs auth, so attach jwtAuth inline
	// (same middleware the galgameAuth group uses) rather than relying on
	// the later group. submit/claim/patch/delete on /:gid are POST/PATCH/
	// DELETE so they don't collide and stay in the auth group below.
	galgame.Get("/mine", jwtAuth, submissionH.ListMine)
	galgame.Get("/:gid", optionalJWT, galgameH.Get)
	// optionalJWT so the visibility gate (AssertGalgameVisible) can let an
	// authenticated submitter see their own pending/declined draft's history,
	// while anonymous callers only ever see published galgames' revisions/PRs.
	galgame.Get("/:gid/revisions", optionalJWT, revisionH.ListRevisions)
	galgame.Get("/:gid/revisions/:rev", optionalJWT, revisionH.GetRevision)
	galgame.Get("/:gid/revisions/:rev/diff", optionalJWT, revisionH.GetRevisionDiff)
	galgame.Get("/:gid/prs", optionalJWT, revisionH.ListPRs)
	galgame.Get("/:gid/prs/:id", optionalJWT, revisionH.GetPR)
	galgame.Get("/:gid/links", linkH.ListLinks)
	galgame.Get("/:gid/aliases", linkH.ListAliases)
	galgame.Get("/:gid/contributors", contributorH.List)
	// Three-source score snapshot (step 34). Public; display-only (no gate).
	galgame.Get("/:gid/scores", galgameH.Scores)

	// ── Cross-service endpoints (OAuth Client Basic Auth) ──
	//
	// MUST be registered BEFORE the `galgameAuth := galgame.Group("", jwtAuth)`
	// line below. Fiber v3's `Group("", middleware)` with an empty prefix is
	// equivalent to `app.Use("/galgame", middleware)` (see fiber/v3/group.go
	// Group ctor: it calls `app.register(methodUse, prefix, …)` when the group
	// has handlers). Use() then matches every `/galgame/*` route registered
	// AFTER it — including ones registered on the parent `galgame` group
	// without `galgameAuth`. So any endpoint that needs non-Bearer auth
	// (Basic / public) MUST land above this fence; otherwise jwtAuth runs
	// first and rejects Basic with code=10002.
	//
	// /messages/feed: kungal/moyu cron pulls wiki messages here via Basic Auth.
	galgame.Get("/messages/feed",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		messageH.ListFeed,
	)
	// /revisions/recent: kungal/moyu cron pulls merged-revision (edit) events
	// here via Basic Auth → mirror into their local activity timelines.
	galgame.Get("/revisions/recent",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		revisionH.RecentRevisions,
	)
	// /taxonomy/recent: kungal/moyu cron pulls taxonomy change events (e.g.
	// series creation) here via Basic Auth → mirror into their local timelines.
	// Filterable by ?entity=&action= (e.g. entity=series&action=created).
	galgame.Get("/taxonomy/recent",
		middleware.OAuthClientBasicAuth(oauthClientRepo),
		taxRevH.RecentFeed,
	)

	// Internal catalog data browser (step 19): staff-only (catalog.review = ren)
	// read-only proxy to the catalog S2S read face — the Basic credentials stay
	// server-side. A non-empty prefix means this group does NOT trip the
	// empty-prefix fence gotcha above; it carries its own jwtAuth + permission gate.
	catalogProxy := galgameHandler.NewCatalogProxyHandler(catalogCli)
	catBrowse := galgame.Group("/catalog", jwtAuth, middleware.RequirePermission(catalogPerm.Resolver, catalogPerm.Review))
	catBrowse.Get("/stats", catalogProxy.Stats)
	catBrowse.Get("/search/entities", catalogProxy.Search)
	catBrowse.Get("/works/:id", catalogProxy.Work)
	catBrowse.Get("/works/:id/credits", catalogProxy.Credits)
	catBrowse.Get("/labels/:id/works", catalogProxy.LabelWorks)

	// ─── Bearer JWT fence — every route below this point inherits jwtAuth ───
	galgameAuth := galgame.Group("", jwtAuth)
	// POST /galgame is the admin direct-publish bypass: it creates a
	// status=0 entry immediately, skipping the user submission queue. Regular
	// users must go through POST /galgame/submit (creates status=3, awaits
	// review). Locked to galgame.create (moderator/admin/ren) so non-staff
	// can't bypass review.
	galgameAuth.Post("/", middleware.RequirePermission(galgamePerm.Resolver, galgamePerm.Create), galgameH.Create)
	galgameAuth.Put("/:gid", galgameH.Update)
	// Canonical galgame image upload (cover + screenshot). Any logged-in user —
	// incl. forum/moyu proxying their users via their wiki client — POSTs
	// multipart {file, preset}; uploads under the wiki image client
	// (site=galgame_wiki) and returns the hash. Centralizing here makes the
	// wiki the single owner of galgame image bytes, so the site-scoped galgame
	// reference-ping covers everything. Single-segment static path, no collision
	// with /:gid (which is GET-only here).
	galgameAuth.Post("/image", galgameH.UploadImage)
	galgameAuth.Post("/:gid/revert", revisionH.Revert)
	galgameAuth.Post("/:gid/prs", revisionH.SubmitPR)
	galgameAuth.Put("/:gid/prs/:id/merge", revisionH.MergePR)
	galgameAuth.Put("/:gid/prs/:id/decline", revisionH.DeclinePR)
	galgameAuth.Post("/:gid/links", linkH.CreateLink)
	galgameAuth.Delete("/:gid/links", linkH.DeleteLink)
	galgameAuth.Post("/:gid/aliases", linkH.CreateAlias)
	galgameAuth.Delete("/:gid/aliases", linkH.DeleteAlias)
	galgameAuth.Delete("/:gid/contributors/:id", contributorH.Delete)

	// ── User submission flow ──
	// GET /mine is registered earlier (before the public /:gid catch-all)
	// — see the comment there. submit/claim are POST and patch/delete are
	// PATCH/DELETE, none of which collide with GET /:gid, so they stay
	// here in the auth group.
	galgameAuth.Post("/submit", submissionH.Submit)
	galgameAuth.Post("/:gid/claim", submissionH.Claim)
	galgameAuth.Patch("/:gid", submissionH.PatchDraft)
	galgameAuth.Delete("/:gid", submissionH.DeleteDraft)

	// ── Messages ──
	// /messages/mine — end-user JWT.
	// /messages/feed is registered ABOVE the jwtAuth fence; see comment there.
	galgameAuth.Get("/messages/mine", messageH.ListMine)

	// ── Admin ──
	// Admin endpoints require both JWT validity AND galgame.admin_access
	// (moderator/admin/ren) — without the permission check anyone with a valid
	// OAuth access_token could modify galgame status.
	admin := api.Group("/admin", jwtAuth, middleware.RequirePermission(galgamePerm.Resolver, galgamePerm.AdminAccess))
	admin.Get("/stats", adminH.Stats)
	admin.Get("/galgame", adminH.ListGalgames)
	admin.Get("/galgame/messages", messageH.ListAdminQueue)
	admin.Get("/galgame/:gid", adminH.GetGalgame)
	admin.Put("/galgame/:gid/status", adminH.UpdateGalgameStatus)
	// Bulk soft-delete all of a user's galgame (severe-spam cleanup;
	// content-side companion to the OAuth anonymize action).
	admin.Post("/galgame/ban-by-user/:userId", adminH.BanGalgamesByUser)

	// ── Tag ──
	// Create: any logged-in user (introduce a tag for original/doujin
	// works missing from VNDB). Update/Delete: admin/moderator (role
	// checked inside the handler, same as series).
	tag := api.Group("/tag")
	tag.Get("/", tagH.List)
	tag.Get("/search", searchH.Tag) // Meilisearch-backed (replaces DB LIKE search)
	tag.Get("/multi", tagH.Multi)
	tag.Get("/:name", tagH.GetByName)
	// Member galgame ids → the forum intersects with local + filters there.
	tag.Get("/:id/galgame-ids", tagH.GalgameIDs)
	tag.Post("/", jwtAuth, tagH.Create)
	tag.Put("/", jwtAuth, tagH.Update)
	tag.Delete("/:id", jwtAuth, tagH.Delete)
	// Tag revision/revert (admin/moderator) — same surface for each of
	// the four taxonomy entities; see TaxonomyRevisionHandler.
	tag.Get("/:id/revisions", taxRevH.TagListRevisions)
	tag.Get("/:id/revisions/:rev", taxRevH.TagGetRevision)
	tag.Post("/:id/revert", jwtAuth, taxRevH.TagRevert)

	// ── Official ──
	official := api.Group("/official")
	official.Get("/", officialH.List)
	official.Get("/search", searchH.Official) // Meilisearch-backed
	official.Get("/:name", officialH.GetByName)
	official.Get("/:id/galgame-ids", officialH.GalgameIDs)
	official.Post("/", jwtAuth, officialH.Create)
	official.Put("/", jwtAuth, officialH.Update)
	official.Delete("/:id", jwtAuth, officialH.Delete)
	official.Get("/:id/revisions", taxRevH.OfficialListRevisions)
	official.Get("/:id/revisions/:rev", taxRevH.OfficialGetRevision)
	official.Post("/:id/revert", jwtAuth, taxRevH.OfficialRevert)

	// ── Engine ──
	engine := api.Group("/engine")
	engine.Get("/", engineH.List)
	engine.Get("/:name", engineH.GetByName)
	engine.Get("/:id/galgame-ids", engineH.GalgameIDs)
	engine.Post("/", jwtAuth, engineH.Create)
	engine.Put("/", jwtAuth, engineH.Update)
	engine.Delete("/:id", jwtAuth, engineH.Delete)
	engine.Get("/:id/revisions", taxRevH.EngineListRevisions)
	engine.Get("/:id/revisions/:rev", taxRevH.EngineGetRevision)
	engine.Post("/:id/revert", jwtAuth, taxRevH.EngineRevert)

	// ── Series ──
	series := api.Group("/series")
	series.Get("/", seriesH.List)
	series.Get("/search", seriesH.Search)
	series.Get("/:id", seriesH.Get)
	series.Get("/:id/revisions", taxRevH.SeriesListRevisions)
	series.Get("/:id/revisions/:rev", taxRevH.SeriesGetRevision)
	seriesAuth := series.Group("", jwtAuth)
	seriesAuth.Post("/", seriesH.Create)
	seriesAuth.Post("/modal", seriesH.Modal)
	seriesAuth.Put("/:id", seriesH.Update)
	seriesAuth.Delete("/:id", seriesH.Delete)
	seriesAuth.Post("/:id/revert", taxRevH.SeriesRevert)

	// ─── NextMoe open API: galgame public projection (/v1/galgame/*) ───
	//
	// A NEW public read-only bypass (step 02) — the internal /api/galgame read
	// face above is untouched (routes + shapes unchanged). Every route is gated
	// by the shared devapi middleware chain (API key → per-minute rate limit →
	// daily quota → galgame:read scope) and metered per response. NSFW is inert
	// in Phase 1 (no key carries galgame:nsfw), so the projection is sfw-only.
	mountPublic(a, cfg, galgameSvc, searchSvc, galgameH, entityGalgamesH)
}
