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

const (
	ScopePlaytimeRead  = devapi.ScopePlaytimeRead
	ScopePlaytimeWrite = devapi.ScopePlaytimeWrite
)

const PlaytimePrefix = "/v1/playtime"

const playtimeMinePageSize = 200

const (
	playtimeRatePerMin = 120
	playtimeRateWindow = time.Minute
)

const ctxKeyScope ctxKey = "catalog:token_scope"

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
			if err == nil && !ok {
				return response.TooManyRequests(c)
			}
		}
		return c.Next()
	}
}

type PlaytimeLimiter interface {
	Allow(ctx context.Context, clientID string, userID int64) (bool, error)
}

type storeLimiter struct {
	store devapi.Store
	limit int64
}

func NewPlaytimeLimiter(store devapi.Store) PlaytimeLimiter {
	if store == nil {
		return nil
	}
	return &storeLimiter{store: store, limit: playtimeRatePerMin}
}

func (l *storeLimiter) Allow(ctx context.Context, clientID string, userID int64) (bool, error) {
	bucket := time.Now().UTC().Unix() / int64(playtimeRateWindow.Seconds())
	key := "playtime:rate:" + clientID + ":" + strconv.FormatInt(userID, 10) +
		":" + strconv.FormatInt(bucket, 10)
	n, err := l.store.Incr(ctx, key, playtimeRateWindow*2)
	if err != nil {
		return true, err
	}
	return n <= l.limit, nil
}

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

func requireScope(ctx context.Context, want string) *houseError {
	scope, _ := ctx.Value(ctxKeyScope).(string)
	if !hasScope(scope, want) {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token is missing the "+want+" scope")
	}
	return nil
}

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
