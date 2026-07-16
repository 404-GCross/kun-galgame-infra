package galgameapp

import (
	"context"
	"log/slog"
	"time"

	"api/internal/app"
	"api/internal/infrastructure/cache"
	"api/internal/platform/devapi"
	galgameHandler "api/internal/platform/galgame/handler"
	galgameSearch "api/internal/platform/galgame/search"
	galgameService "api/internal/platform/galgame/service"
	"api/pkg/config"

	"github.com/gofiber/fiber/v3"
)

// mountPublic mounts the /v1/galgame public projection group behind the devapi
// middleware chain, wires per-response usage metering, and starts the usage
// flush lifecycle (60s ticker + a final flush on graceful shutdown, run before
// the main DB is closed). Taxonomy / calendar / reverse-lookup endpoints are
// whitelisted passthroughs of the existing serving handlers; the aggregate list
// / detail / batch / search / changes are the new frozen projection.
func mountPublic(
	a *app.App,
	cfg *config.Config,
	galgameSvc *galgameService.GalgameService,
	searchSvc *galgameSearch.Service,
	galgameH *galgameHandler.GalgameHandler,
	entityGalgamesH *galgameHandler.EntityGalgamesHandler,
) {
	oauthDB := a.DB.DB() // kun_galgame_infra — the developer-platform tables

	// devapi counter/cache store: reuse the shared Redis when reachable, else
	// fail open (rate-limit/quota degrade to allow — the public read face stays
	// available; the DB credential check never fails open). Built here rather than
	// via app NeedCache so a Redis outage can NEVER block the galgame service from
	// booting or affect the internal read face.
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

	publicH := galgameHandler.NewPublicHandler(galgameSvc, searchSvc)

	// Meter every response to (client, key, "galgame", day) + async last-used
	// touch. Placed right after ResolveCredential so a 401 (no/invalid key) is not
	// billed, while a 429/403 (authenticated-but-limited) IS captured.
	recordUsage := func(c fiber.Ctx) error {
		err := c.Next()
		if cred := devapi.CredentialFrom(c); cred != nil {
			usageRec.Record(cred, "galgame", c.Response().StatusCode())
			go usageRec.TouchLastUsed(context.Background(), cred)
		}
		return err
	}
	// Force the content_limit gate on passthrough routes that honor the param, so
	// a caller can't pass content_limit=all/nsfw to reach NSFW on the sfw face.
	// ResolveContentLimit is always "sfw" in Phase 1 (no key holds galgame:nsfw).
	sfwGate := func(c fiber.Ctx) error {
		c.Request().URI().QueryArgs().Set("content_limit", devapi.ResolveContentLimit(c, c.Query("content_limit")))
		return c.Next()
	}

	v1 := a.Fiber.Group("/v1/galgame",
		mw.ResolveCredential,
		recordUsage,
		mw.RateLimit,
		mw.Quota,
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

	// Usage flush lifecycle: a 60s ticker upserts the in-memory rollup; a final
	// flush runs on graceful shutdown via OnPreShutdown, which fires during
	// Fiber.Shutdown() BEFORE the main DB is closed (app.Run order), so the last
	// batch is not lost. TouchLastUsed / Record are best-effort.
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
}
