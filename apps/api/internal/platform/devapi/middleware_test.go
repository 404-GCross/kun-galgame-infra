package devapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// TestRateLimitBoundary pins the 60th request passing and the 61st being
// blocked within one minute bucket (free tier = 60/min).
func TestRateLimitBoundary(t *testing.T) {
	m := NewMiddleware(nil, newMemStore())
	cred := &Credential{KeyID: 1, Tier: TierFree}
	now := time.Now()
	for i := 1; i <= 61; i++ {
		limit, _, _, allowed, failOpen := m.rateResult(context.Background(), cred, now)
		if failOpen {
			t.Fatalf("req %d: unexpected fail-open", i)
		}
		if limit != 60 {
			t.Fatalf("req %d: limit = %d, want 60", i, limit)
		}
		if i <= 60 && !allowed {
			t.Fatalf("req %d should be allowed", i)
		}
		if i == 61 && allowed {
			t.Fatalf("req 61 should be blocked")
		}
	}
}

// TestRateLimitFailOpen: a store outage fails open (裁定 5).
func TestRateLimitFailOpen(t *testing.T) {
	store := newMemStore()
	store.failing = true
	m := NewMiddleware(nil, store)
	cred := &Credential{KeyID: 1, Tier: TierFree}
	_, _, _, _, failOpen := m.rateResult(context.Background(), cred, time.Now())
	if !failOpen {
		t.Fatalf("expected fail-open when store is down")
	}
}

// TestRateLimitUnlimited: internal tier is never rate-limited.
func TestRateLimitUnlimited(t *testing.T) {
	m := NewMiddleware(nil, newMemStore())
	cred := &Credential{KeyID: 1, Tier: TierInternal}
	limit, _, _, allowed, failOpen := m.rateResult(context.Background(), cred, time.Now())
	if failOpen || !allowed || limit != 0 {
		t.Fatalf("internal tier = (limit %d, allowed %v, failOpen %v), want (0,true,false)", limit, allowed, failOpen)
	}
}

// TestQuotaRollover: the daily counter resets at the day boundary (a new day is
// a distinct key). Uses a small override to exhaust cheaply.
func TestQuotaRollover(t *testing.T) {
	m := NewMiddleware(nil, newMemStore())
	cred := &Credential{KeyID: 2, Tier: TierFree, QuotaOverride: 2}
	day1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	day2 := day1.AddDate(0, 0, 1)

	for i := 1; i <= 3; i++ {
		_, _, allowed, _ := m.quotaResult(context.Background(), cred, day1)
		if want := i <= 2; allowed != want {
			t.Fatalf("day1 req %d: allowed = %v, want %v", i, allowed, want)
		}
	}
	// New day → counter resets → first request allowed again.
	if _, _, allowed, _ := m.quotaResult(context.Background(), cred, day2); !allowed {
		t.Fatalf("day2 first request should be allowed (rollover reset)")
	}
}

// TestRateLimitHeadersAnd429 drives the full RateLimit handler through Fiber to
// pin the response headers + Retry-After on overflow.
func TestRateLimitHeadersAnd429(t *testing.T) {
	m := NewMiddleware(nil, newMemStore())
	cred := &Credential{KeyID: 3, Tier: TierFree}

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		c.Locals(credLocalsKey, cred)
		return c.Next()
	}, m.RateLimit, func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	var last int
	var retryAfter, limitHdr string
	for i := 1; i <= 61; i++ {
		resp, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatalf("req %d: %v", i, err)
		}
		last = resp.StatusCode
		retryAfter = resp.Header.Get("Retry-After")
		limitHdr = resp.Header.Get("X-RateLimit-Limit")
	}
	if last != fiber.StatusTooManyRequests {
		t.Errorf("61st status = %d, want 429", last)
	}
	if retryAfter == "" {
		t.Errorf("429 missing Retry-After header")
	}
	if limitHdr != "60" {
		t.Errorf("X-RateLimit-Limit = %q, want 60", limitHdr)
	}
}

// TestRequireScope: a missing scope yields 403, a present one passes.
func TestRequireScope(t *testing.T) {
	build := func(scopes []string) *fiber.App {
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			c.Locals(credLocalsKey, &Credential{Scopes: scopes})
			return c.Next()
		}, RequireScope(ScopeGalgameRead), func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusOK)
		})
		return app
	}

	resp, _ := build([]string{ScopeCatalogRead}).Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusForbidden {
		t.Errorf("missing scope status = %d, want 403", resp.StatusCode)
	}
	resp, _ = build([]string{ScopeCatalogRead, ScopeGalgameRead}).Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("present scope status = %d, want 200", resp.StatusCode)
	}
}

// TestExtractKeyQuadrants pins the dual-credential transport rule (裁定 6): the
// key is taken from Authorization: Bearer ONLY when it carries our nm_ prefix,
// otherwise the request falls through to X-API-Key. Covers all four credential
// quadrants (key only / JWT only / both / neither) plus the bearer-key-wins
// precedence — proving existing single-credential behavior is unchanged and the
// only new path is the dual-credential one.
func TestExtractKeyQuadrants(t *testing.T) {
	const key = "nm_live_ABC123def456"
	// A representative non-key Bearer value (a user JWT). No nm_ prefix.
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig"

	cases := []struct {
		name string
		auth string // Authorization header value ("" = unset)
		xkey string // X-API-Key header value ("" = unset)
		want string
	}{
		{"key in bearer only (legacy)", "Bearer " + key, "", key},
		{"key in x-api-key only (legacy)", "", key, key},
		{"jwt in bearer only → no key", "Bearer " + jwt, "", ""},
		{"dual: key in x-api-key + jwt in bearer", "Bearer " + jwt, key, key},
		{"neither", "", "", ""},
		{"bearer key wins over x-api-key", "Bearer " + key, "nm_live_other", key},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			var got string
			app.Get("/", func(c fiber.Ctx) error {
				got = extractKey(c)
				return c.SendStatus(fiber.StatusOK)
			})
			req := httptest.NewRequest("GET", "/", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.xkey != "" {
				req.Header.Set("X-API-Key", tc.xkey)
			}
			if _, err := app.Test(req); err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			if got != tc.want {
				t.Errorf("extractKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRequireTier: a missing credential is 401, a wrong tier is 403, a matching
// tier passes (mirrors RequireScope). Fences the /internal face to internal tier.
func TestRequireTier(t *testing.T) {
	build := func(setCred func(c fiber.Ctx)) *fiber.App {
		app := fiber.New()
		app.Get("/", func(c fiber.Ctx) error {
			setCred(c)
			return c.Next()
		}, RequireTier(TierInternal), func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusOK)
		})
		return app
	}

	resp, _ := build(func(c fiber.Ctx) {}).Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("no credential status = %d, want 401", resp.StatusCode)
	}
	for _, tier := range []string{TierFree, TierTrusted} {
		resp, _ = build(func(c fiber.Ctx) { c.Locals(credLocalsKey, &Credential{Tier: tier}) }).
			Test(httptest.NewRequest("GET", "/", nil))
		if resp.StatusCode != fiber.StatusForbidden {
			t.Errorf("%s tier status = %d, want 403", tier, resp.StatusCode)
		}
	}
	resp, _ = build(func(c fiber.Ctx) { c.Locals(credLocalsKey, &Credential{Tier: TierInternal}) }).
		Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusOK {
		t.Errorf("internal tier status = %d, want 200", resp.StatusCode)
	}
}

// TestContentLimitGate: nsfw requires both the scope and the nsfw_allowed flag;
// anything short downgrades to sfw (Phase 1 has no nsfw key, so this is the
// inert-but-wired default-safe path).
func TestContentLimitGate(t *testing.T) {
	run := func(cred *Credential, requested string) string {
		app := fiber.New()
		var got string
		app.Get("/", func(c fiber.Ctx) error {
			c.Locals(credLocalsKey, cred)
			got = ResolveContentLimit(c, requested)
			return c.SendStatus(fiber.StatusOK)
		})
		_, _ = app.Test(httptest.NewRequest("GET", "/", nil))
		return got
	}

	if cl := run(&Credential{}, "nsfw"); cl != "sfw" {
		t.Errorf("no scope/flag: content_limit = %q, want sfw", cl)
	}
	if cl := run(&Credential{NSFWAllowed: true}, "nsfw"); cl != "sfw" {
		t.Errorf("flag but no scope: content_limit = %q, want sfw", cl)
	}
	if cl := run(&Credential{NSFWAllowed: true, Scopes: []string{ScopeGalgameNSFW}}, "nsfw"); cl != "nsfw" {
		t.Errorf("scope+flag: content_limit = %q, want nsfw", cl)
	}
	if cl := run(&Credential{NSFWAllowed: true, Scopes: []string{ScopeGalgameNSFW}}, "sfw"); cl != "sfw" {
		t.Errorf("requested sfw: content_limit = %q, want sfw", cl)
	}

	// W1a three-state (P5): `all` is gated identically to `nsfw`.
	authorized := &Credential{NSFWAllowed: true, Scopes: []string{ScopeGalgameNSFW}}
	if cl := run(authorized, "all"); cl != "all" {
		t.Errorf("scope+flag requesting all: content_limit = %q, want all", cl)
	}
	// A no-scope key must resolve to sfw for EVERY requested value — so its
	// projection is byte-identical across sfw/nsfw/all (G3 no-scope invariant).
	for _, req := range []string{"sfw", "nsfw", "all", ""} {
		if cl := run(&Credential{}, req); cl != "sfw" {
			t.Errorf("no-scope key requesting %q: content_limit = %q, want sfw", req, cl)
		}
	}
	// flag-but-no-scope requesting all → still sfw.
	if cl := run(&Credential{NSFWAllowed: true}, "all"); cl != "sfw" {
		t.Errorf("flag but no scope requesting all: content_limit = %q, want sfw", cl)
	}
}

// TestResolveCacheHit: a positive cache entry short-circuits the DB (repo nil).
func TestResolveCacheHit(t *testing.T) {
	store := newMemStore()
	m := NewMiddleware(nil, store) // nil repo — a cache hit must not reach it
	raw := "nm_live_cachehit"
	want := &Credential{KeyID: 7, ClientID: "c1", Tier: TierTrusted, Scopes: []string{ScopeCatalogRead}}
	b, _ := json.Marshal(want)
	store.kv["devkey:"+hashHex(raw)] = b

	got, err := m.resolve(context.Background(), raw)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || got.KeyID != 7 || got.Tier != TierTrusted {
		t.Fatalf("resolve returned %+v, want KeyID 7 / trusted", got)
	}
}

// TestResolveCacheNegative: a negative cache entry resolves to 401 (nil,nil)
// without touching the DB.
func TestResolveCacheNegative(t *testing.T) {
	store := newMemStore()
	m := NewMiddleware(nil, store)
	raw := "nm_live_cachemiss"
	store.kv["devkey:"+hashHex(raw)] = []byte{credCacheNeg}

	got, err := m.resolve(context.Background(), raw)
	if err != nil || got != nil {
		t.Fatalf("resolve = (%+v, %v), want (nil, nil)", got, err)
	}
}
