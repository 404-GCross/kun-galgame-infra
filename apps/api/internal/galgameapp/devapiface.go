package galgameapp

import (
	"context"
	"log/slog"
	"time"

	"api/internal/app"
	"api/internal/infrastructure/cache"
	"api/internal/platform/devapi"
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

// mountInternal mounts the /internal rich read face: the 44 GET read routes of
// the internal /api/{galgame,tag,official,engine,series} face, byte-identical,
// behind the shared devapi chain with a RequireTier(internal) gate (free/trusted
// keys → 403). The chain order mirrors the /v1 chain with two changes:
// RequireTier is inserted after recordUsage, and NO sfwGate is attached —
// content_limit passes through untouched, which is the byte-compat semantics
// itself (the internal face must serve NSFW rows to downstream consumers).
//
// RateLimit/Quota are kept on the chain for uniformity even though the internal
// tier is unlimited (TierLimits → no-op). This face is READ-ONLY: writes,
// admin, S2S feeds and the catalog proxy are NOT exposed here (out of scope).
func mountInternal(a *app.App, face devapiFace, reads readRoutes) {
	internal := a.Fiber.Group("/internal",
		face.mw.ResolveCredential,
		face.recordUsage("galgame_internal"),
		devapi.RequireTier(devapi.TierInternal),
		face.mw.RateLimit,
		face.mw.Quota,
		devapi.RequireScope(devapi.ScopeGalgameRead),
	)
	reads.register(internal)
}
