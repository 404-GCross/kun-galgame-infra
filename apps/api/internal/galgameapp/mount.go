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
	// ClaimCatalog mints a freshly PUBLISHED galgame's catalog identity right
	// after the product commit (wave 146), so a new entry is registry-visible
	// immediately instead of after the nightly reconcile. Built at the assembly
	// point from catalogsync.Hook (it needs both pools); nil leaves the nightly
	// job as the sole registrar. The status-TRANSITION half of the same fix
	// lives on the editing engine's galgame.game OnMerge hook, wired next to
	// the engine itself.
	ClaimCatalog galgameService.ClaimHookFunc
}

// Mount wires the galgame domain (repositories, services, handlers) and registers
// its full HTTP surface — the /api staff face (admin/ban + the taxonomy
// tag/official/engine/series CRUD+revert family + the staff catalog-browser
// proxy), the devapi-gated /internal face carrying ALL user reads (galgame:read)
// and user writes (galgame:write), and the S2S cron feeds — onto a.Fiber. The
// NextMoe /v1/galgame public projection was DELISTED at wave 146 (2026-07-30);
// its prefix is now a 410 catch-all (publicgone.go).
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

	// Services
	galgameSvc := galgameService.NewGalgameService(galgameRepository, revisionRepo, prRepo, userReadRepo).
		WithCDNBase(cfg.ImageService.CDNBase).
		WithEditing(deps.Edit, editq).
		WithClaimHook(deps.ClaimCatalog)
	submissionSvc := galgameService.NewSubmissionService(galgameRepository, messageRepo).
		WithEditing(deps.Edit).
		WithClaimHook(deps.ClaimCatalog)
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
	engineH := galgameHandler.NewEngineHandler(engineRepo, taxSvc)
	seriesH := galgameHandler.NewSeriesHandler(seriesRepo, taxSvc)
	// One handler covers ListRevisions / GetRevision / Revert for all
	// four taxonomy entities — the entity discriminator is baked into
	// the route wrapper methods (TagListRevisions / OfficialRevert / …).
	taxRevH := galgameHandler.NewTaxonomyRevisionHandler(taxSvc)
	// A2-1e area B: the staff taxonomy READ-BACK pairs and the /internal
	// ownership-meta batch. Both are pure reads over the existing repositories.
	// The picker query is shared by the staff door (/api, edit_any) and the
	// contributor door (/internal, any signed-in user) — A2-1g.
	taxPicker := galgameService.NewTaxonomyPicker(tagRepo, officialRepo, engineRepo, seriesRepo)
	staffTaxH := galgameHandler.NewStaffTaxonomyHandler(tagRepo, officialRepo, engineRepo, seriesRepo, taxPicker)
	contribTaxH := galgameHandler.NewContributorTaxonomyHandler(taxPicker)
	metaH := galgameHandler.NewGalgameMetaHandler(galgameRepository)
	adminH := galgameHandler.NewAdminHandler(adminRepo, adminSvc, searchHook)
	submissionH := galgameHandler.NewSubmissionHandler(submissionSvc, searchHook, imgCli)
	messageH := galgameHandler.NewMessageHandler(messageSvc)
	searchH := galgameHandler.NewSearchHandler(searchSvc)

	// JWT auth middleware — backed by an accept-both verifier (ES256/RS256 via
	// the OP's JWKS + legacy HS256). HS256-only when KUN_OIDC_JWKS_URL is unset.
	// See docs/auth/03-oidc-standardization-design.md §10 Phase 1.
	tokenVerifier := oidctoken.NewVerifierWithJWKS(cfg.JWT.Secret, cfg.OIDC.JWKSURL)
	jwtAuth := middleware.JWTAuth(tokenVerifier)
	// OptionalJWT — populates user_id when a valid Bearer token is present, but
	// never blocks the request. Used on the /internal workflow search so
	// anonymous callers still get status=0-only results while authenticated ones
	// additionally see their own pending/declined drafts.
	optionalJWT := middleware.OptionalJWT(tokenVerifier)

	// ── Registrar for the devapi-gated /internal platform-workflow face ──
	// Since 09-open-api-phase2 route-B endgame W5 retired the 29 A/C-bucket public
	// reads to the /v1 contract, workflowRoutes.register backs the surviving 15
	// platform-workflow routes only (see mountInternal + workflowroutes.go for the
	// per-route charter). The handler/service instances are the same ones the /api
	// staff face + /v1 public face + /internal write face use — no handler logic is
	// duplicated; the workflow struct simply carries the subset those 15 routes
	// need (the entity-galgames / link / contributor / tag/official/engine/series
	// list handlers the retired reads used are no longer wired onto this face).
	workflow := workflowRoutes{
		galgameH:    galgameH,
		searchH:     searchH,
		submissionH: submissionH,
		messageH:    messageH,
		taxRevH:     taxRevH,
		contribTaxH: contribTaxH,
		optionalJWT: optionalJWT,
		jwtAuth:     jwtAuth,
	}

	// API routes. Since 09-open-api-phase2 wave 06a W3 the /api face is
	// STAFF-ONLY: the admin/ban routes, the taxonomy CRUD+revert family
	// (tag/official/engine + series), and the staff catalog-browser proxy. The
	// 44 GET reads + S2S feeds moved to the /internal read face back in wave 05
	// A2; W3 then retired the 12 user-write registrations this group used to
	// carry, so ALL user reads AND writes now live on the devapi-gated /internal
	// face — reads under scope galgame:read (mountInternal), writes under scope
	// galgame:write (mountInternalWrites). The proposal face is 06b.
	api := a.Fiber.Group("/api")

	// ── Staff catalog-browser proxy (under /api/galgame/catalog) ──
	galgame := api.Group("/galgame")

	// Internal catalog data browser (step 19): staff-only (catalog.review = ren)
	// read-only proxy to the catalog S2S read face — the Basic credentials stay
	// server-side. It carries its own jwtAuth + permission gate on a non-empty
	// /catalog prefix, distinct from the taxonomy/admin staff groups below.
	catalogProxy := galgameHandler.NewCatalogProxyHandler(catalogCli)
	catBrowse := galgame.Group("/catalog", jwtAuth, middleware.RequirePermission(catalogPerm.Resolver, catalogPerm.Review))
	catBrowse.Get("/stats", catalogProxy.Stats)
	catBrowse.Get("/search/entities", catalogProxy.Search)
	catBrowse.Get("/works/:id", catalogProxy.Work)
	catBrowse.Get("/works/:id/credits", catalogProxy.Credits)
	catBrowse.Get("/labels/:id/works", catalogProxy.LabelWorks)

	// ── User writes retired (09-open-api-phase2 06a W3) ──
	// The 12 jwtAuth-gated user-write routes that used to live here (the
	// galgameAuth Bearer group: Create/Update/UploadImage, links/aliases ×4,
	// contributor delete, and the submission flow Submit/Claim/PatchDraft/
	// DeleteDraft) were retired in W3. Their sole host is now the devapi-gated
	// /internal user-write face (mountInternalWrites), which pairs the client key
	// (scope galgame:write) with the same jwtAuth user identity. The empty-prefix
	// Bearer fence is gone with them; the catalog proxy above keeps its own gate.
	// GET /messages/mine and the S2S GET /messages/feed had already moved to the
	// /internal read face in wave 05 A2.

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

	// ── Tag writes ── (GET reads live on the /internal read face since A2)
	// Create: any logged-in user (introduce a tag for original/doujin works
	// missing from VNDB). Update/Delete/revert: admin/moderator (role checked
	// inside the handler, same as series).
	// A2-1e: the READ-BACK pair. `/search` is registered BEFORE `/:id` — Fiber
	// matches in registration order, so a static segment must precede the param
	// route that would otherwise swallow it (the same fence every group here
	// keeps).
	tag := api.Group("/tag")
	tag.Get("/search", jwtAuth, staffTaxH.TagSearch)
	tag.Get("/:id", jwtAuth, staffTaxH.TagDetail)
	tag.Post("/", jwtAuth, tagH.Create)
	tag.Put("/", jwtAuth, tagH.Update)
	tag.Delete("/:id", jwtAuth, tagH.Delete)
	tag.Post("/:id/revert", jwtAuth, taxRevH.TagRevert)

	// ── Official writes ── (GET reads live on the /internal read face since A2)
	official := api.Group("/official")
	official.Get("/search", jwtAuth, staffTaxH.OfficialSearch)
	official.Get("/:id", jwtAuth, staffTaxH.OfficialDetail)
	official.Post("/", jwtAuth, officialH.Create)
	official.Put("/", jwtAuth, officialH.Update)
	official.Delete("/:id", jwtAuth, officialH.Delete)
	official.Post("/:id/revert", jwtAuth, taxRevH.OfficialRevert)

	// ── Engine writes ── (GET reads live on the /internal read face since A2)
	engine := api.Group("/engine")
	engine.Get("/search", jwtAuth, staffTaxH.EngineSearch)
	engine.Get("/:id", jwtAuth, staffTaxH.EngineDetail)
	engine.Post("/", jwtAuth, engineH.Create)
	engine.Put("/", jwtAuth, engineH.Update)
	engine.Delete("/:id", jwtAuth, engineH.Delete)
	engine.Post("/:id/revert", jwtAuth, taxRevH.EngineRevert)

	// ── Series writes ── (GET reads live on the /internal read face since A2)
	series := api.Group("/series")
	seriesAuth := series.Group("", jwtAuth)
	seriesAuth.Get("/search", staffTaxH.SeriesSearch)
	seriesAuth.Get("/:id", staffTaxH.SeriesDetail)
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
	//   /internal   = the internal-tier platform-workflow face: the 15 surviving
	//     workflow routes (mine/messages-mine/search[SearchWithPending]/drafts/
	//     user×3 + taxonomy revisions×8), gated by RequireTier(internal); NO
	//     sfwGate (content_limit passes through untouched). Since 09-open-api-phase2
	//     route-B endgame W5 the 29 public data reads retired to /v1; this face
	//     ALSO carries the two S2S cron feeds (/galgame/messages/feed +
	//     /galgame/revisions/recent) on the same devapi chain. Downstream
	//     kungal/moyu/letmoe S2S consumers.
	//   /internal (writes) = the SOLE user-write face (09-open-api-phase2 06a):
	//     the 12 jwtAuth-gated write handlers (Create/Update/UploadImage,
	//     links/aliases ×4, contributor delete, submission Submit/Claim/
	//     PatchDraft/DeleteDraft), behind the devapi write chain (scope
	//     galgame:write, metered under galgame_internal_write). W3 retired the
	//     legacy /api Bearer registrations, so this face is now their only host.
	//     Registered BEFORE mountInternal so the read face's Group Use does not
	//     blanket it (see mountInternalWrites).
	//
	// The fourth face this used to build — /v1/galgame, the public third-party
	// projection — was delisted at wave 146 and no longer rides this chain; its
	// prefix is a credential-free 410 catch-all (mountRetiredPublic).
	face := newDevapiFace(a, cfg)
	writes := writeRoutes{
		galgameH:     galgameH,
		linkH:        linkH,
		contributorH: contributorH,
		submissionH:  submissionH,
	}
	// The /internal/edit/* platform proposal face (09-open-api-phase2 06b): the
	// editing engine's user-proposal subset, actor derived from the verified JWT,
	// filing tenant reverse-looked-up from the key's client binding
	// (oauth_clients.catalog_site, read from the OAuth DB). Registered BETWEEN the
	// write face and the read face's Group Use so its per-route chain is not
	// blanketed (same ordering rule as mountInternalWrites).
	proposeH := newProposeHandler(deps.Edit, siteRepo.NewOAuthClientRepository(oauthDB))
	mountInternalWrites(a, face, writes, jwtAuth)
	mountInternalPropose(a, face, proposeH, jwtAuth)
	mountInternal(a, face, workflow, messageH, revisionH, metaH)
	mountRetiredPublic(a)
}
