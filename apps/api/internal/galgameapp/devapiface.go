package galgameapp

import (
	"context"
	"log/slog"
	"time"

	"api/internal/app"
	"api/internal/infrastructure/cache"
	"api/internal/platform/devapi"
	galgameHandler "api/internal/platform/galgame/handler"
	"api/pkg/config"

	"github.com/gofiber/fiber/v3"
)

// devapiFace bundles the developer-platform middleware chain collaborators that
// every devapi-gated face in this process shares: the /v1 public projection and
// the /internal rich read face. It is built ONCE (newDevapiFace) so there is a
// single usage-flush ticker and a single OnPreShutdown Redis Close — never a
// second of either.
type devapiFace struct {
	mw       *devapi.Middleware
	usageRec *devapi.UsageRecorder
}

// newDevapiFace builds the shared devapi collaborators over the main DB and a
// best-effort Redis store, then installs the usage-flush lifecycle: a 60s ticker
// upsert plus a final flush on graceful shutdown (OnPreShutdown fires during
// Fiber.Shutdown() BEFORE the main DB is closed, so the last batch is not lost)
// and the Redis Close. Call EXACTLY once per process.
//
// The devapi cache/counter store reuses the shared Redis when reachable, else
// fails open (rate-limit/quota degrade to allow — the read faces stay available;
// the DB credential check never fails open). Built here rather than via app
// NeedCache so a Redis outage can never block the service from booting.
func newDevapiFace(a *app.App, cfg *config.Config) devapiFace {
	oauthDB := a.DB.DB() // kun_galgame_infra — the developer-platform tables

	var devCache *cache.RedisCache
	if rc, err := cache.NewRedisCache(cfg.Redis); err != nil {
		slog.Warn("devapi: redis unavailable — rate-limit/quota will fail open", "err", err)
	} else {
		devCache = rc
	}
	store := devapi.NewRedisStore(devCache)
	repo := devapi.NewRepository(oauthDB)
	mw := devapi.NewMiddleware(repo, store)
	usageRec := devapi.NewUsageRecorder(repo, store)

	flushDone := make(chan struct{})
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-flushDone:
				return
			case <-t.C:
				if err := usageRec.Flush(context.Background()); err != nil {
					slog.Warn("devapi usage flush failed", "err", err)
				}
			}
		}
	}()
	a.Fiber.Hooks().OnPreShutdown(func() error {
		close(flushDone)
		if err := usageRec.Flush(context.Background()); err != nil {
			slog.Warn("devapi final usage flush failed", "err", err)
		}
		if devCache != nil {
			_ = devCache.Close()
		}
		return nil
	})

	return devapiFace{mw: mw, usageRec: usageRec}
}

// recordUsage returns a metering middleware tagged with the given surface
// ("galgame" for /v1, "galgame_internal" for /internal). Placed right after
// ResolveCredential so a 401 (no/invalid key) is not billed, while a 429/403
// (authenticated-but-limited) IS captured.
func (f devapiFace) recordUsage(surface string) fiber.Handler {
	return func(c fiber.Ctx) error {
		err := c.Next()
		if cred := devapi.CredentialFrom(c); cred != nil {
			f.usageRec.Record(cred, surface, c.Response().StatusCode())
			go f.usageRec.TouchLastUsed(context.Background(), cred)
		}
		return err
	}
}

// mountInternal mounts the /internal platform-workflow face: the 16 GET routes
// registered by workflowRoutes.register (galgame /mine, /messages/mine, /search
// [SearchWithPending], /drafts, /user/:id/{stats,galgames,contributed},
// /taxonomy/:family/search; the four taxonomy families' /:id/revisions(/:rev))
// plus the machine-facing reads
// registered directly below (the two S2S cron feeds and, since A2-1e, the
// ownership-meta batch), behind the shared devapi chain with
// a RequireTier(internal) gate (free/trusted keys → 403). The chain order mirrors
// the /v1 chain with two changes: RequireTier is inserted after recordUsage, and
// NO sfwGate is attached — content_limit passes through untouched.
//
// Since 09-open-api-phase2 route-B endgame W5 this face is NO LONGER the host of
// the public galgame data reads: the 29 A/C-bucket reads (list/detail/batch/
// taxonomy-search/calendar/stats/check/relations + taxonomy list/search/by-id)
// retired to the frozen public /v1/galgame contract. What remains is the
// re-chartered platform workflow surface — a designed dual-credential surface in
// the same family as the write (06a) and proposal (06b) faces, NOT wiki legacy;
// see workflowroutes.go for the per-route charter. Its metering deliberately
// stays "galgame_internal".
//
// RateLimit/Quota are kept on the chain for uniformity even though the internal
// tier is unlimited (TierLimits → no-op). This face is READ-ONLY: writes,
// admin and the catalog proxy are NOT exposed here (out of scope).
//
// It ALSO mounts the two S2S cron feeds — /galgame/messages/feed and
// /galgame/revisions/recent — which downstream kungal/moyu/letmoe cron pull with
// the nm_ key (the key IS the identity — no Basic auth): scope galgame:read,
// internal tier, metered under galgame_internal. Wave 05 A1 收编 them here off
// the legacy /api Basic-auth face; A2 then retired those /api registrations; they
// survive W5 by charter. The feeds are registered directly here and deliberately
// NOT through workflow.register — that helper is the user-context workflow
// projection, and the S2S feeds are machine-sync primitives, not part of it.
// /taxonomy/recent is NOT mounted: the post-mortem (V2) confirmed it a dead
// route, so A2 dropped it outright rather than migrating it.
// mountInternalWrites mounts the /internal user-write face: the 12 jwtAuth-gated
// write routes (galgame Create/Update/UploadImage, links/aliases ×4, contributor
// delete, submission Submit/Claim/PatchDraft/DeleteDraft). Since 09-open-api-phase2
// wave 06a W3 retired the legacy /api Bearer group, this devapi-gated face is the
// SOLE host of these writes, reached with the dual-credential convention
// (X-API-Key = client identity; Authorization: Bearer = the end-user JWT).
//
// The chain is the /internal read chain's write variant: same ResolveCredential
// → RateLimit → Quota shape, but metered under a distinct face
// (galgame_internal_write, D4 — write traffic must be independently observable),
// gated on ScopeGalgameWrite instead of ScopeGalgameRead, and terminating in the
// SAME jwtAuth Mount builds (the user identity model is unchanged — D5). There is
// no sfwGate (writes carry no content_limit).
//
// CRITICAL ordering: this MUST be called BEFORE mountInternal. mountInternal
// installs Group("/internal", readChain...), which in Fiber is a prefix Use that
// blankets every /internal/* route registered after it. Registering the writes
// (with their chain attached PER ROUTE — see writeRoutes.register) first keeps
// the read chain from blanketing them, while the route-level write handlers
// blanket nothing themselves. The two faces never collide on method (reads are
// GET; writes POST/PUT/PATCH/DELETE).
func mountInternalWrites(a *app.App, face devapiFace, writes writeRoutes, jwtAuth fiber.Handler) {
	chain := []fiber.Handler{
		face.mw.ResolveCredential,
		face.recordUsage("galgame_internal_write"),
		devapi.RequireTier(devapi.TierInternal),
		face.mw.RateLimit,
		face.mw.Quota,
		devapi.RequireScope(devapi.ScopeGalgameWrite),
		jwtAuth,
	}
	// The parent is a PLAIN group (no group middleware) — the chain rides each
	// route so it never becomes a blanket /internal Use.
	writes.register(a.Fiber.Group("/internal"), chain)
}

func mountInternal(a *app.App, face devapiFace, workflow workflowRoutes, messageH *galgameHandler.MessageHandler, revisionH *galgameHandler.RevisionHandler, metaH *galgameHandler.GalgameMetaHandler) {
	internal := a.Fiber.Group("/internal",
		face.mw.ResolveCredential,
		face.recordUsage("galgame_internal"),
		devapi.RequireTier(devapi.TierInternal),
		face.mw.RateLimit,
		face.mw.Quota,
		devapi.RequireScope(devapi.ScopeGalgameRead),
	)
	// Feeds first: /galgame/messages/feed + /galgame/revisions/recent are static
	// multi-segment paths. The /galgame/:gid read catch-all that once made this
	// ordering load-bearing was retired in W5, but registration order is app-stack-
	// wide in Fiber and the feeds share the /galgame group with the workflow face's
	// /messages/mine, so keeping them first stays the safe, self-documenting choice.
	// They are registered directly here (NOT through workflow.register) because the
	// S2S feeds are machine-sync primitives, not part of the user-context workflow
	// projection — same separation the old read face kept.
	ifeeds := internal.Group("/galgame")
	ifeeds.Get("/messages/feed", messageH.ListFeed)
	ifeeds.Get("/revisions/recent", revisionH.RecentRevisions)
	// A2-1e: the ownership-meta batch (status-blind {gid,user_id,status} for the
	// forum edit lane's owner assertion). Static path, registered with the other
	// machine-facing reads and ahead of the workflow group for the same reason
	// they are.
	ifeeds.Get("/meta", metaH.Meta)

	workflow.register(internal)
}
