package handler

import (
	"context"
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"

	siteModel "api/internal/platform/site/model"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

const (
	localClient = "catalog:oauth_client"
)

type ctxKey string

const (
	ctxKeyClient  ctxKey = "catalog:oauth_client"
	ctxKeyAdminID ctxKey = "catalog:admin_user_id"
)

func S2SAuth(clients *siteRepo.OAuthClientRepository) fiber.Handler {
	return func(c fiber.Ctx) error {
		client, err := authenticateBasic(c, clients)
		if err != nil {
			return response.Unauthorized(c, errors.ErrAuthUnauthorized)
		}
		c.Locals(localClient, client)
		return c.Next()
	}
}

func authenticateBasic(c fiber.Ctx, clients *siteRepo.OAuthClientRepository) (*siteModel.OAuthClient, error) {
	header := c.Get("Authorization")
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return nil, stderrors.New("not Basic auth")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return nil, err
	}
	clientID, secret, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil, stderrors.New("malformed Basic auth")
	}
	client, err := clients.FindByClientID(c.Context(), clientID)
	if err != nil || client == nil {
		return nil, stderrors.New("bad client")
	}
	if !client.VerifySecret(secret) {
		return nil, stderrors.New("bad secret")
	}
	return client, nil
}

func S2SBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if client, ok := fc.Locals(localClient).(*siteModel.OAuthClient); ok {
		ctx = huma.WithValue(ctx, ctxKeyClient, client)
	}
	next(ctx)
}

func clientFromCtx(ctx context.Context) *siteModel.OAuthClient {
	c, _ := ctx.Value(ctxKeyClient).(*siteModel.OAuthClient)
	return c
}

func enforceSiteBinding(client *siteModel.OAuthClient, site string) *houseError {
	if client == nil || client.CatalogSite == "" {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"client is not bound to a catalog site; it cannot claim works")
	}
	if client.CatalogSite != site {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			fmt.Sprintf("client is bound to site %q and cannot claim for site %q", client.CatalogSite, site))
	}
	return nil
}

func isThirdPartyClient(client *siteModel.OAuthClient) bool {
	return client != nil && client.OwnerUserID != nil
}

func AdminBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyAdminID, int64(id))
	}
	next(ctx)
}

func adminIDFromCtx(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKeyAdminID).(int64)
	return id
}
