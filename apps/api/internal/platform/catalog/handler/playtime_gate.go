// playtime_gate.go — the playtime face's authorization chain.
//
// This face authenticates with a USER ACCESS TOKEN and nothing else. That is a
// deliberate departure from the open API's other doors, which lead with a
// machine API key, and the reasoning is worth stating because the opposite
// choice is the obvious one:
//
// A client-bound access token already carries BOTH identities this face needs —
// `id` says which human, `client_id` says which application. An API key
// alongside it would prove the application a second time, in a second registry,
// against a second scope list. Two registries holding the same word is how you
// get an app whose key allows playtime while its client does not, and a support
// question nobody can answer without reading both tables. So: one credential,
// one scope check, one place to revoke.
//
// What the key chain does provide — metering, rate limiting, quota — is not
// free here, so this file carries the piece that actually matters for a write
// face: a per-(application, user) rate limit. It is keyed on the token's own
// client_id, which is the same axis the key chain would have used, so when
// metering is generalized off the API key the two will already agree.
package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"api/internal/platform/devapi"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// The playtime scopes, re-exported from the package that decides who may
// request them (devapi owns the self-service consent allow-list). Naming them
// twice would let the gate and the allow-list drift into disagreeing about a
// string, which is exactly the failure this face is designed around.
const (
	ScopePlaytimeRead  = devapi.ScopePlaytimeRead
	ScopePlaytimeWrite = devapi.ScopePlaytimeWrite
)

// PlaytimePrefix is the mount point of the playtime face. It sits under /v1
// with the rest of the open API — this is a product surface a third-party app
// author discovers in the developer portal, not an internal face — and is
// disjoint from /v1/catalog so the key chain in front of that group never
// intercepts a playtime call.
const PlaytimePrefix = "/v1/playtime"

// playtimeMinePageSize caps one sync page. A 300-game library is two pages.
const playtimeMinePageSize = 200

// Per-(application, user) write limits. The window is a minute because that is
// the unit a launcher's sync loop is written in; the ceiling is high enough
// that a first-login library sync (200 works per batch call) never touches it
// and low enough that a runaway loop stops before it becomes our problem.
const (
	playtimeRatePerMin = 120
	playtimeRateWindow = time.Minute
)

const ctxKeyScope ctxKey = "catalog:token_scope"

// PlaytimeGate is the face's authorization middleware, applied after
// middleware.JWTAuth (which has already rejected an absent/invalid token).
//
// It proves three things and leaves the fourth to the ops:
//
//  1. the token names a user — a playtime belongs to somebody;
//  2. the token is client-bound — the application id is the report's third key
//     member and the handle a misbehaving app is excluded by, so a first-party
//     /auth/login token (which carries no client_id) is refused here;
//  3. the token carries at least one playtime scope.
//
// Which of the two scopes an operation needs is the operation's own business —
// see requireScope. Checking "at least one" here keeps a token with no playtime
// grant out of the face entirely, so the per-op check is a second line rather
// than the only one.
//
// Note what is NOT required: any binding to a catalog site. The catalog write
// planes resolve a tenant from oauth_clients.catalog_site because every row
// they write belongs to a product site. A playtime belongs to a user and an
// app. Demanding a catalog tenant would shut out every third-party launcher
// this face exists for.
func PlaytimeGate(limiter PlaytimeLimiter) fiber.Handler {
	return func(c fiber.Ctx) error {
		uid, _ := c.Locals("user_id").(uint)
		if uid == 0 {
			return response.UnauthorizedMsg(c, errors.ErrAuthUnauthorized,
				"the access token carries no user identity")
		}

		scope, _ := c.Locals("user_scope").(string)
		if !hasScope(scope, ScopePlaytimeRead) && !hasScope(scope, ScopePlaytimeWrite) {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is missing the "+ScopePlaytimeRead+" / "+ScopePlaytimeWrite+" scope")
		}

		clientID, _ := c.Locals("token_client_id").(string)
		if clientID == "" {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is not bound to an OAuth client; the playtime face requires a client-bound token")
		}

		if limiter != nil {
			ok, err := limiter.Allow(c.Context(), clientID, int64(uid))
			// A limiter outage must not take the face down with it: the
			// counter is a guard against runaway clients, not an
			// authorization decision, and failing closed here would turn a
			// Redis blip into "nobody can record their playtime".
			if err == nil && !ok {
				return response.TooManyRequests(c)
			}
		}
		return c.Next()
	}
}

// PlaytimeLimiter is the per-(application, user) write throttle. The narrow
// interface is what lets the gate be tested without Redis, and what will let
// the general metering layer replace the implementation without touching this
// file.
type PlaytimeLimiter interface {
	// Allow reports whether this (client, user) pair may perform one more
	// write in the current window. An error means the backend is unavailable;
	// the caller treats that as "allow" on purpose (see PlaytimeGate).
	Allow(ctx context.Context, clientID string, userID int64) (bool, error)
}

// storeLimiter implements PlaytimeLimiter over the same counter store the open
// API's rate limiter uses, so there is one Redis and one failure mode.
type storeLimiter struct {
	store devapi.Store
	limit int64
}

// NewPlaytimeLimiter builds the default limiter. A nil store yields a nil
// limiter, which PlaytimeGate reads as "no throttling configured" — the shape
// a unit test and a store-less deployment both want.
func NewPlaytimeLimiter(store devapi.Store) PlaytimeLimiter {
	if store == nil {
		return nil
	}
	return &storeLimiter{store: store, limit: playtimeRatePerMin}
}

func (l *storeLimiter) Allow(ctx context.Context, clientID string, userID int64) (bool, error) {
	// The window is encoded in the key rather than tracked, so an expired
	// window simply stops being addressed — no sweep, no clock skew between
	// counter and TTL.
	bucket := time.Now().UTC().Unix() / int64(playtimeRateWindow.Seconds())
	key := "playtime:rate:" + clientID + ":" + strconv.FormatInt(userID, 10) +
		":" + strconv.FormatInt(bucket, 10)
	n, err := l.store.Incr(ctx, key, playtimeRateWindow*2)
	if err != nil {
		return true, err
	}
	return n <= l.limit, nil
}

// PlaytimeBridge lifts the token's uid, its client id and its scope string into
// the Huma context. All three come from the verified token; none can be
// influenced by the request body.
func PlaytimeBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyUserID, int64(id))
	}
	if scope, ok := fc.Locals("user_scope").(string); ok {
		ctx = huma.WithValue(ctx, ctxKeyScope, scope)
	}
	if clientID, ok := fc.Locals("token_client_id").(string); ok {
		ctx = huma.WithValue(ctx, ctxKeyPlaytimeClient, clientID)
	}
	next(ctx)
}

const ctxKeyPlaytimeClient ctxKey = "catalog:playtime_client"

// requireScope is the per-op half of the scope check: the gate proved the token
// carries one of the two, this proves it carries the right one.
func requireScope(ctx context.Context, want string) *houseError {
	scope, _ := ctx.Value(ctxKeyScope).(string)
	if !hasScope(scope, want) {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token is missing the "+want+" scope")
	}
	return nil
}

// playtimeActor resolves who is reporting and from which application. Both
// values come from the token; there is no field on the wire to lie in.
func playtimeActor(ctx context.Context) (uid int64, clientID string, he *houseError) {
	uid = userIDFromCtx(ctx)
	if uid <= 0 {
		return 0, "", apiErrMsg(http.StatusUnauthorized, errors.ErrAuthUnauthorized,
			"the access token carries no user identity")
	}
	clientID, _ = ctx.Value(ctxKeyPlaytimeClient).(string)
	if clientID == "" {
		return 0, "", apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token is not bound to an OAuth client")
	}
	return uid, clientID, nil
}
