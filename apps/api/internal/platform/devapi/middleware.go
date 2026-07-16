package devapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// credLocalsKey is the Fiber locals key under which ResolveCredential stashes
// the resolved *Credential for downstream middleware/handlers.
const credLocalsKey = "devapi_cred"

// Resolve-cache tuning. A positive credential is cached 60s (裁定 1); a negative
// result is cached briefly to blunt lookup floods for an unknown/rejected key
// without meaningfully delaying a legitimately re-enabled one.
const (
	credCachePosTTL = 60 * time.Second
	credCacheNegTTL = 10 * time.Second
	credCacheNeg    = byte('-') // one-byte negative marker (a JSON Credential is longer)
)

// Middleware carries the collaborators the open-API request chain needs. The
// host process (cmd/catalog — both the galgame and catalog public faces) wires
// it in 02/03; this package only delivers + tests the handlers.
type Middleware struct {
	repo  *Repository
	store Store
}

// NewMiddleware builds the middleware chain over the main-DB repository and the
// counter/cache store.
func NewMiddleware(repo *Repository, store Store) *Middleware {
	return &Middleware{repo: repo, store: store}
}

// CredentialFrom returns the resolved credential a preceding ResolveCredential
// stored, or nil.
func CredentialFrom(c fiber.Ctx) *Credential {
	cred, _ := c.Locals(credLocalsKey).(*Credential)
	return cred
}

// ResolveCredential authenticates the request from its API key (Authorization:
// Bearer nm_… or X-API-Key), resolving it against the main DB with a 60s Redis
// cache. On any missing/invalid credential it returns 401; on a genuine DB
// error it returns 503 and does NOT fail open (裁定 5).
func (m *Middleware) ResolveCredential(c fiber.Ctx) error {
	raw := extractKey(c)
	if !HasKeyPrefix(raw) {
		return resp401(c)
	}
	cred, err := m.resolve(c.Context(), raw)
	if err != nil {
		slog.Error("devapi credential resolve failed", "err", err)
		return response.Error(c, fiber.StatusServiceUnavailable, errors.ErrInternalServer, "credential store unavailable")
	}
	if cred == nil {
		return resp401(c)
	}
	c.Locals(credLocalsKey, cred)
	return c.Next()
}

// resolve computes the key hash, consults the cache, and falls back to the DB,
// caching the positive/negative outcome. A nil credential means "no valid
// credential" (401); a non-nil error means an infra failure (do not fail open).
func (m *Middleware) resolve(ctx context.Context, raw string) (*Credential, error) {
	cacheKey := "devkey:" + hashHex(raw)
	if b, _ := m.store.Get(ctx, cacheKey); len(b) > 0 {
		if len(b) == 1 && b[0] == credCacheNeg {
			return nil, nil
		}
		var cred Credential
		if json.Unmarshal(b, &cred) == nil {
			return &cred, nil
		}
		// Corrupt cache entry → fall through to the DB.
	}

	cred, err := m.repo.ResolveByHash(ctx, HashKey(raw), time.Now())
	if err != nil {
		return nil, err
	}
	if cred == nil {
		_ = m.store.Set(ctx, cacheKey, []byte{credCacheNeg}, credCacheNegTTL)
		return nil, nil
	}
	if b, err := json.Marshal(cred); err == nil {
		_ = m.store.Set(ctx, cacheKey, b, credCachePosTTL)
	}
	return cred, nil
}

// RateLimit enforces the per-minute request budget (per key, shared across
// faces) with a minute-bucketed counter (`ratelimit:{key_id}:{minute}`). Sets
// the X-RateLimit-* headers; on overflow returns 429 + Retry-After. If the
// counter store is unavailable it fails open with a WARN (裁定 5).
func (m *Middleware) RateLimit(c fiber.Ctx) error {
	cred := CredentialFrom(c)
	if cred == nil {
		return resp401(c)
	}
	limit, remaining, reset, allowed, failOpen := m.rateResult(c.Context(), cred, time.Now())
	if failOpen {
		slog.Warn("devapi rate-limit store unavailable; failing open", "key_id", cred.KeyID)
		return c.Next()
	}
	if limit > 0 { // 0 == unlimited (internal tier): no headers, always allowed
		c.Set("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		c.Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
	}
	if !allowed {
		retry := reset - time.Now().UTC().Unix()
		if retry < 1 {
			retry = 1
		}
		c.Set("Retry-After", strconv.FormatInt(retry, 10))
		return resp429(c)
	}
	return c.Next()
}

// rateResult runs the counter math for `now`. Returns the effective limit,
// remaining (clamped ≥0), the reset epoch second, whether the request is
// allowed, and whether the store failed (caller fails open). An unlimited tier
// yields limit 0 / allowed true.
func (m *Middleware) rateResult(ctx context.Context, cred *Credential, now time.Time) (limit, remaining int, reset int64, allowed, failOpen bool) {
	lim, unlimited := cred.EffectiveRate()
	if unlimited {
		return 0, 0, 0, true, false
	}
	minute := now.UTC().Unix() / 60
	key := fmt.Sprintf("ratelimit:%d:%d", cred.KeyID, minute)
	n, err := m.store.Incr(ctx, key, 65*time.Second)
	if err != nil {
		return 0, 0, 0, false, true
	}
	reset = (minute + 1) * 60
	remaining = lim - int(n)
	if remaining < 0 {
		remaining = 0
	}
	return lim, remaining, reset, int(n) <= lim, false
}

// Quota enforces the daily request quota (per key, shared across faces) with a
// day-bucketed counter (`quota:{key_id}:{YYYY-MM-DD}`, TTL to the next day).
// Sets X-Quota-* headers; on overflow returns 429. Fails open on store outage.
func (m *Middleware) Quota(c fiber.Ctx) error {
	cred := CredentialFrom(c)
	if cred == nil {
		return resp401(c)
	}
	limit, remaining, allowed, failOpen := m.quotaResult(c.Context(), cred, time.Now())
	if failOpen {
		slog.Warn("devapi quota store unavailable; failing open", "key_id", cred.KeyID)
		return c.Next()
	}
	if limit > 0 {
		c.Set("X-Quota-Limit", strconv.Itoa(limit))
		c.Set("X-Quota-Remaining", strconv.Itoa(remaining))
	}
	if !allowed {
		return resp429(c)
	}
	return c.Next()
}

// quotaResult runs the daily-counter math for `now`. A new day is a distinct
// key, so the counter resets automatically at the day boundary.
func (m *Middleware) quotaResult(ctx context.Context, cred *Credential, now time.Time) (limit, remaining int, allowed, failOpen bool) {
	lim, unlimited := cred.EffectiveQuota()
	if unlimited {
		return 0, 0, true, false
	}
	utc := now.UTC()
	key := fmt.Sprintf("quota:%d:%s", cred.KeyID, utc.Format("2006-01-02"))
	n, err := m.store.Incr(ctx, key, ttlUntilNextDay(utc))
	if err != nil {
		return 0, 0, false, true
	}
	remaining = lim - int(n)
	if remaining < 0 {
		remaining = 0
	}
	return lim, remaining, int(n) <= lim, false
}

// RequireScope gates a route on a single scope being present in the credential.
func RequireScope(scope string) fiber.Handler {
	return func(c fiber.Ctx) error {
		cred := CredentialFrom(c)
		if cred == nil {
			return resp401(c)
		}
		if !cred.HasScope(scope) {
			return response.ForbiddenMsg(c, errors.ErrForbidden, "missing required scope: "+scope)
		}
		return c.Next()
	}
}

// ResolveContentLimit computes the effective content_limit for a request. The
// nsfw path requires BOTH the galgame:nsfw scope and the credential's effective
// nsfw_allowed flag; anything short of that downgrades to sfw (default-safe
// projection, not a hard 403). Phase 1 issues no key carrying galgame:nsfw, so
// this always returns "sfw" today — the gate is wired but inert (裁定 6).
func ResolveContentLimit(c fiber.Ctx, requested string) string {
	if requested != "nsfw" {
		return "sfw"
	}
	cred := CredentialFrom(c)
	if cred != nil && cred.NSFWAllowed && cred.HasScope(ScopeGalgameNSFW) {
		return "nsfw"
	}
	return "sfw"
}

// extractKey pulls the raw key from Authorization: Bearer … (preferred) or the
// X-API-Key header (compat).
func extractKey(c fiber.Ctx) string {
	if h := c.Get("Authorization"); h != "" {
		if v, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(v)
		}
	}
	return strings.TrimSpace(c.Get("X-API-Key"))
}

// ttlUntilNextDay is the duration from now (UTC) to the start of the next day,
// plus a small buffer so the counter key survives just past midnight.
func ttlUntilNextDay(now time.Time) time.Duration {
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return next.Sub(now) + time.Minute
}

func resp401(c fiber.Ctx) error {
	return response.Unauthorized(c, errors.ErrAuthUnauthorized)
}

func resp429(c fiber.Ctx) error {
	return response.TooManyRequests(c)
}
