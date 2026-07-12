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

// TestSetIdentityLocals pins the shared fill helper that all three JWT fill
// sites route through (JWTAuth, OptionalJWT, and — DB-backed — Auth): it
// publishes the additive user_global_roles (the GLOBAL roles, NOT the site
// union) and token_client_id, while user_roles keeps its established union
// meaning. This is the whole of what Auth's fill site does with the claims, so
// it covers that site without a main-DB fixture.
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

// TestJWTMiddlewaresFillScopeLocals confirms both verifier-backed fill sites
// (JWTAuth and OptionalJWT) publish the additive scope locals from a real token.
func TestJWTMiddlewaresFillScopeLocals(t *testing.T) {
	const secret = "test-secret"
	verifier := oidctoken.NewVerifier(secret, nil) // HS256-only (no JWKS)
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
