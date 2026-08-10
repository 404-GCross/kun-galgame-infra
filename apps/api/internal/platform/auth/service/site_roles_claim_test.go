package service

import (
	"context"
	"testing"
	"time"

	"api/pkg/oidctoken"
	"api/pkg/utils"
)

func TestSiteRolesClaimBothWireModes(t *testing.T) {
	const secret = "test-secret"
	claims := utils.TokenClaims{
		UserUUID:  "u",
		ID:        7,
		Roles:     []string{"creator"},
		SiteRoles: []string{"moderator"},
		SiteID:    42,
	}

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
