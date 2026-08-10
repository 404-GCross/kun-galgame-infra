package middleware

import (
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"api/pkg/oidctoken"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

func TestSetIdentityLocals(t *testing.T) {
	var gotGlobal, gotUnion []string
	var gotClient string
	app := fiber.New()
	app.Get("/probe", func(c fiber.Ctx) error {
		setIdentityLocals(c, &utils.TokenClaims{
			ID: 42, Roles: []string{"admin"}, SiteRoles: []string{"moderator"}, ClientID: "cli-9",
		})
		gotGlobal, _ = c.Locals("user_global_roles").([]string)
		gotUnion, _ = c.Locals("user_roles").([]string)
		gotClient, _ = c.Locals("token_client_id").(string)
		return c.SendStatus(fiber.StatusOK)
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/probe", nil))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	_ = resp.Body.Close()

	if !slices.Equal(gotGlobal, []string{"admin"}) {
		t.Errorf("user_global_roles = %v, want [admin] (global only, not the union)", gotGlobal)
	}
	if !slices.Equal(gotUnion, []string{"admin", "moderator"}) {
		t.Errorf("user_roles = %v, want the union [admin moderator] (unchanged)", gotUnion)
	}
	if gotClient != "cli-9" {
		t.Errorf("token_client_id = %q, want cli-9", gotClient)
	}
}

func TestJWTMiddlewaresFillScopeLocals(t *testing.T) {
	const secret = "test-secret"
	verifier := oidctoken.NewVerifier(secret, nil)
	token, err := utils.GenerateAccessToken(secret, utils.TokenClaims{
		ID: 1, Roles: []string{"moderator"}, ClientID: "cli-x",
	}, time.Hour)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}

	for name, mw := range map[string]fiber.Handler{
		"JWTAuth":     JWTAuth(verifier),
		"OptionalJWT": OptionalJWT(verifier),
	} {
		var gotGlobal []string
		var gotClient string
		app := fiber.New()
		app.Get("/p", mw, func(c fiber.Ctx) error {
			gotGlobal, _ = c.Locals("user_global_roles").([]string)
			gotClient, _ = c.Locals("token_client_id").(string)
			return c.SendStatus(fiber.StatusOK)
		})
		req := httptest.NewRequest("GET", "/p", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_ = resp.Body.Close()

		if !slices.Equal(gotGlobal, []string{"moderator"}) {
			t.Errorf("%s: user_global_roles = %v, want [moderator]", name, gotGlobal)
		}
		if gotClient != "cli-x" {
			t.Errorf("%s: token_client_id = %q, want cli-x", name, gotClient)
		}
	}
}
