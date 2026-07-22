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
	"api/internal/platform/editing"
	"api/internal/platform/galgame/editquery"
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
	// Edit is the editing engine with galgame.game registered (E2a strangler
	// — every galgame revision/PR write flows through it); EditDB is the
	// engine-table pool (catalog DB) the native edit-log reads (editquery:
	// the merged-revision feed + profile counters) query.
	// Both are REQUIRED: the legacy revision tables are frozen and the
	// surface cannot run without the engine.
	Edit   *editing.Engine
	EditDB *gorm.DB
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

	// E2a: the editing engine is the galgame surface's single revision/PR
	// write path — a missing engine means silent data loss, so fail the boot.
	if deps.Edit == nil || deps.EditDB == nil {
		panic("galgameapp: Deps.Edit and Deps.EditDB are required (editing-engine strangler)")
	}
	editq := editquery.New(deps.EditDB)

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
		WithCDNBase(cfg.ImageService.CDNBase).
		WithEditing(deps.Edit, editq)
	submissionSvc := galgameService.NewSubmissionService(galgameRepository, messageRepo).
		WithEditing(deps.Edit)
	messageSvc := galgameService.NewMessageService(messageRepo, galgameRepository, userReadRepo)
	adminSvc := galgameService.NewAdminService(galgameRepository, messageRepo).
		WithEditing(deps.Edit)
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
	revisionH := galgameHandler.NewRevisionHandler(galgameSvc)
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

	// ── Read-route registrar (shared by the /api internal face and the
	// devapi-gated /internal rich read face) ──
	// The SAME handler/service instances back both faces; readRoutes.register
	// owns the read-route registration order (static/multi-segment + /mine before
	// the galgame /:gid catch-all; taxonomy /search before /:name; series /search
	// before /:id) so it can never drift between the two faces.
	reads := readRoutes{
		galgameH:        galgameH,
		searchH:         searchH,
		entityGalgamesH: entityGalgamesH,
		linkH:           linkH,
		contributorH:    contributorH,
		submissionH:     submissionH,
		messageH:        messageH,
		tagH:            tagH,
		officialH:       officialH,
		engineH:         engineH,
		seriesH:         seriesH,
		taxRevH:         taxRevH,
		optionalJWT:     optionalJWT,
		jwtAuth:         jwtAuth,
	}

	// API routes
	api := a.Fiber.Group("/api")
	// Reads first: every GET read route across galgame/tag/official/engine/
	// series. Registering them here — before the write/feed/proxy routes and
	// their empty-prefix jwtAuth fences below — preserves the original ordering,
	// because a Fiber empty-prefix fence only covers routes registered AFTER it;
	// the reads registered here stay outside every write fence.
	reads.register(api)

	// ── Galgame writes / S2S feeds / catalog proxy ──
	galgame := api.Group("/galgame")

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
	// Revert + PR submit/merge/decline retired with apps/wiki at E3b — user
	// edits now flow through the engine (kungal's edit BFF → catalog edit face).
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
	// GET /messages/mine (end-user JWT) is a read route, registered via
	// reads.register above — before the /:gid catch-all and this fence.
	// /messages/feed is Basic-Auth, registered ABOVE the fence; see there.

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

	// ── Tag writes ── (GET reads registered via reads.register above)
	// Create: any logged-in user (introduce a tag for original/doujin works
	// missing from VNDB). Update/Delete/revert: admin/moderator (role checked
	// inside the handler, same as series).
	tag := api.Group("/tag")
	tag.Post("/", jwtAuth, tagH.Create)
	tag.Put("/", jwtAuth, tagH.Update)
	tag.Delete("/:id", jwtAuth, tagH.Delete)
	tag.Post("/:id/revert", jwtAuth, taxRevH.TagRevert)

	// ── Official writes ── (GET reads registered via reads.register above)
	official := api.Group("/official")
	official.Post("/", jwtAuth, officialH.Create)
	official.Put("/", jwtAuth, officialH.Update)
	official.Delete("/:id", jwtAuth, officialH.Delete)
	official.Post("/:id/revert", jwtAuth, taxRevH.OfficialRevert)

	// ── Engine writes ── (GET reads registered via reads.register above)
	engine := api.Group("/engine")
	engine.Post("/", jwtAuth, engineH.Create)
	engine.Put("/", jwtAuth, engineH.Update)
	engine.Delete("/:id", jwtAuth, engineH.Delete)
	engine.Post("/:id/revert", jwtAuth, taxRevH.EngineRevert)

	// ── Series writes ── (GET reads registered via reads.register above)
	series := api.Group("/series")
	seriesAuth := series.Group("", jwtAuth)
	seriesAuth.Post("/", seriesH.Create)
	seriesAuth.Post("/modal", seriesH.Modal)
	seriesAuth.Put("/:id", seriesH.Update)
	seriesAuth.Delete("/:id", seriesH.Delete)
	seriesAuth.Post("/:id/revert", taxRevH.SeriesRevert)

	// ─── Shared devapi infra (built ONCE) + the two gated read faces ───
	//
	// newDevapiFace builds the middleware chain + usage recorder + flush
	// lifecycle a SINGLE time; both faces register against it — no second flush
	// ticker, no second OnPreShutdown Redis Close.
	//   /internal   = the internal-tier rich read face: the 44 read routes
	//     registered above, byte-identical, gated by RequireTier(internal); NO
	//     sfwGate (content_limit passes through untouched). Since 09-open-api-
	//     phase2 wave 05 A1 it ALSO carries the two S2S cron feeds
	//     (/galgame/messages/feed + /galgame/revisions/recent) on the same devapi
	//     chain — the legacy /api Basic-auth feed registrations above are left
	//     untouched (A2 retires them). Downstream kungal/moyu/letmoe S2S consumers.
	//   /v1/galgame = the public third-party projection (frozen contract).
	face := newDevapiFace(a, cfg)
	mountInternal(a, face, reads, messageH, revisionH)
	mountPublic(a, face, galgameSvc, searchSvc, galgameH, entityGalgamesH)
}
