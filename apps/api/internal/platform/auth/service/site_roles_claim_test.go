package service

import (
	"context"
	"testing"
	"time"

	"api/pkg/oidctoken"
	"api/pkg/utils"
)

// TestSiteRolesClaimBothWireModes proves the site_roles claim carries
// identically through both token-signing paths — the legacy HS256 (utils) and
// the oidctoken signer used once asymmetric signing is on. Both serialize the
// whole TokenClaims struct, so the field is signer-independent; this pins that,
// plus the omitted-when-empty shape.
func TestSiteRolesClaimBothWireModes(t *testing.T) {
	const secret = "test-secret"
	claims := utils.TokenClaims{
		UserUUID:  "u",
		ID:        7,
		Roles:     []string{"creator"},
		SiteRoles: []string{"moderator"},
		SiteID:    42,
	}

	// Legacy HS256 path.
	legacy, err := utils.GenerateAccessToken(secret, claims, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := utils.ParseToken(legacy, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SiteRoles) != 1 || got.SiteRoles[0] != "moderator" {
		t.Errorf("legacy: SiteRoles = %v, want [moderator]", got.SiteRoles)
	}

	// oidctoken signer path (the asymmetric path uses this same Signer
	// interface).
	tok, err := oidctoken.NewHS256Signer(secret, "https://id.example").SignAccess(claims, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := oidctoken.NewVerifier(secret, nil).Parse(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2.SiteRoles) != 1 || got2.SiteRoles[0] != "moderator" {
		t.Errorf("signer: SiteRoles = %v, want [moderator]", got2.SiteRoles)
	}

	// Omitted when empty — a non-site-bound token carries no site_roles.
	noSite := utils.TokenClaims{UserUUID: "u", ID: 7, Roles: []string{"user"}}
	nt, err := utils.GenerateAccessToken(secret, noSite, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ng, err := utils.ParseToken(nt, secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(ng.SiteRoles) != 0 {
		t.Errorf("no-site: SiteRoles = %v, want empty", ng.SiteRoles)
	}
}
