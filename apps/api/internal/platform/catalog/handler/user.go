package handler

import (
	"context"
	"net/http"
	"strings"

	siteModel "api/internal/platform/site/model"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

const ScopeCatalogEdit = "catalog:edit"

const UserPrefix = "/api/v1/user/catalog"

const (
	ctxKeyUserID    ctxKey = "catalog:user_id"
	ctxKeyUserRoles ctxKey = "catalog:user_roles"
)

type OAuthClientLookup interface {
	FindByClientID(ctx context.Context, clientID string) (*siteModel.OAuthClient, error)
}

func UserGate(clients OAuthClientLookup) fiber.Handler {
	return func(c fiber.Ctx) error {
		uid, _ := c.Locals("user_id").(uint)
		if uid == 0 {
			return response.UnauthorizedMsg(c, errors.ErrAuthUnauthorized,
				"the access token carries no user identity")
		}

		scope, _ := c.Locals("user_scope").(string)
		if !hasScope(scope, ScopeCatalogEdit) {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is missing the "+ScopeCatalogEdit+" scope")
		}

		clientID, _ := c.Locals("token_client_id").(string)
		if clientID == "" {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is not bound to an OAuth client; the catalog user plane requires a client-bound token")
		}
		client, err := clients.FindByClientID(c.Context(), clientID)
		if err != nil || client == nil {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token's client is not registered")
		}
		if client.CatalogSite == "" {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token's client is not bound to a catalog site")
		}

		c.Locals(localClient, client)
		return c.Next()
	}
}

func hasScope(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}

func UserBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyUserID, int64(id))
	}
	if roles, ok := fc.Locals("user_roles").([]string); ok {
		ctx = huma.WithValue(ctx, ctxKeyUserRoles, roles)
	}
	if client, ok := fc.Locals(localClient).(*siteModel.OAuthClient); ok {
		ctx = huma.WithValue(ctx, ctxKeyClient, client)
	}
	next(ctx)
}

func userIDFromCtx(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKeyUserID).(int64)
	return id
}

func userRolesFromCtx(ctx context.Context) []string {
	roles, _ := ctx.Value(ctxKeyUserRoles).([]string)
	return roles
}

func userActor(ctx context.Context) (uid int64, site string, he *houseError) {
	uid = userIDFromCtx(ctx)
	if uid <= 0 {
		return 0, "", apiErrMsg(http.StatusUnauthorized, errors.ErrAuthUnauthorized,
			"the access token carries no user identity")
	}
	client := clientFromCtx(ctx)
	if client == nil || client.CatalogSite == "" {
		return 0, "", apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token's client is not bound to a catalog site")
	}
	return uid, client.CatalogSite, nil
}
