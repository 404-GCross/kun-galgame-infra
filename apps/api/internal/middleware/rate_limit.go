package middleware

import (
	"encoding/json"
	"time"

	"api/internal/infrastructure/cache"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/limiter"
)

// oauthTokenRatePerMin is the per-client ceiling on POST /oauth/token.
//
// /oauth/token is itself authenticated (confidential clients present a
// 64-char client_secret; public clients prove possession via PKCE +
// rotating refresh_token; authorization codes are one-time, 10-min TTL).
// Brute force is infeasible against any of these, so this limit is NOT an
// anti-credential-stuffing control — it is purely a circuit breaker against
// a runaway/buggy client (e.g. an infinite refresh loop) hammering the
// server. Keyed per client_id so one site's burst can't starve another.
//
// 6000/min = 100 token-ops/sec per client. A site where every active user
// refreshes once per 15 min sustains N/900 ops/sec; 100/sec covers ~1.5M
// concurrently-active users plus login bursts — comfortably above any real
// load while still tripping on a pathological loop.
const oauthTokenRatePerMin = 6000

// RateLimit middleware limits request rate per IP.
//
// This is a crude anti-abuse backstop for ANONYMOUS traffic only. It is
// keyed by bare IP — which is fundamentally wrong for the confidential-
// client SSR-proxy architecture: kungal/moyu proxy every one of their
// users' requests through a single backend IP, so a per-IP cap would
// throttle the entire site to 100 req/min (causing mass refresh failures
// → users kicked to login). The Next() guard below exempts any request
// that carries an Authorization header: those are authenticated machine
// (Basic) or user (Bearer) calls whose abuse is bounded by the actual auth
// middleware downstream (a forged header just yields 401, not data) and,
// for /oauth/token specifically, by the per-client OAuthTokenRateLimit.
// Genuinely anonymous traffic (login, register, password reset, public
// reads) still gets the 100/min/IP ceiling plus endpoint-level StrictRateLimit.
func RateLimit(redisCache *cache.RedisCache) fiber.Handler {
	config := limiter.Config{
		Max:        100,
		Expiration: 1 * time.Minute,
		Next: func(c fiber.Ctx) bool {
			return c.Get("Authorization") != ""
		},
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    errors.ErrTooManyRequests,
				"message": errors.GetMessage(errors.ErrTooManyRequests),
			})
		},
	}

	// Use redis storage if available
	if storage := redisCache.Storage(); storage != nil {
		config.Storage = storage
	}

	return limiter.New(config)
}

// OAuthTokenRateLimit is the rate limiter for POST /oauth/token.
//
// Replaces the old StrictRateLimit (10/min keyed by IP+path) which was
// catastrophically wrong here: confidential SSR clients (kungal/moyu)
// proxy every user's token exchange + 15-min refresh through one backend
// IP, so 10/min throttled the entire site → cascading 429 → users kicked.
//
// This limiter is keyed by client_id (parsed from the JSON body) so each
// site gets its own generous bucket; an unparseable/absent client_id
// falls back to a per-IP key so a malformed flood is still bounded.
// See oauthTokenRatePerMin for why the ceiling is deliberately high.
func OAuthTokenRateLimit(redisCache *cache.RedisCache) fiber.Handler {
	config := limiter.Config{
		Max:        oauthTokenRatePerMin,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			// c.Body() is the fasthttp-buffered request body — safe to
			// read here without consuming it for the downstream handler's
			// c.Bind().JSON(). Parse only the client_id field.
			var body struct {
				ClientID string `json:"client_id"`
			}
			if err := json.Unmarshal(c.Body(), &body); err == nil && body.ClientID != "" {
				return "tokc:" + body.ClientID
			}
			return "tokip:" + c.IP()
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    errors.ErrTooManyRequests,
				"message": errors.GetMessage(errors.ErrTooManyRequests),
			})
		},
	}

	if storage := redisCache.Storage(); storage != nil {
		config.Storage = storage
	}

	return limiter.New(config)
}

// StrictRateLimit creates a stricter rate limiter for sensitive endpoints
func StrictRateLimit(redisCache *cache.RedisCache) fiber.Handler {
	config := limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP() + ":" + c.Path()
		},
		LimitReached: func(c fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"code":    errors.ErrTooManyRequests,
				"message": errors.GetMessage(errors.ErrTooManyRequests),
			})
		},
	}

	// Use redis storage if available
	if storage := redisCache.Storage(); storage != nil {
		config.Storage = storage
	}

	return limiter.New(config)
}
