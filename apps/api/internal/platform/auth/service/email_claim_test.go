package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"api/pkg/oidctoken"
	"api/pkg/utils"
)

// rawClaims decodes a JWS payload without verifying it. The typed round-trip
// can't tell "" apart from an absent key, and the whole point of gating the
// claim is that it must not be on the wire at all — so assert the wire shape.
func rawClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWS (%d segments)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestEmailClaimRespectsScope pins the access token's half of the email gate.
// The claim used to ship unconditionally, so a client granted only
// `openid profile` could base64-decode the token and read the address that
// /oauth/userinfo withholds — the filter was bypassable without any secret.
//
// Both signing paths are checked: legacy HS256 (utils) and the oidctoken Signer
// used under asymmetric / standard-wire mode. Both serialize the whole
// TokenClaims struct, so the gate is wire-mode-independent by construction.
func TestEmailClaimRespectsScope(t *testing.T) {
	const secret = "test-secret"
	const addr = "kun@kungal.com"

	// Exactly how the authorization-code and refresh paths fill the claim.
	build := func(scope string) utils.TokenClaims {
		return utils.TokenClaims{
			UserUUID: "u",
			ID:       7,
			Scope:    scope,
			Email:    EmailForScope(scope, addr),
		}
	}

	t.Run("granted scope carries the address", func(t *testing.T) {
		claims := build("openid profile email")

		legacy, err := utils.GenerateAccessToken(secret, claims, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		got, err := utils.ParseToken(legacy, secret)
		if err != nil {
			t.Fatal(err)
		}
		if got.Email != addr {
			t.Errorf("legacy: Email = %q, want %q", got.Email, addr)
		}

		signed, err := oidctoken.NewHS256Signer(secret, "https://id.example").SignAccess(claims, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		got2, err := oidctoken.NewVerifier(secret, nil).Parse(context.Background(), signed)
		if err != nil {
			t.Fatal(err)
		}
		if got2.Email != addr {
			t.Errorf("signer: Email = %q, want %q", got2.Email, addr)
		}
	})

	t.Run("withheld scope omits the claim on both paths", func(t *testing.T) {
		claims := build("openid profile")

		legacy, err := utils.GenerateAccessToken(secret, claims, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, present := rawClaims(t, legacy)["email"]; present {
			t.Error("legacy: `email` key is on the wire; omitempty should have dropped it")
		}

		signed, err := oidctoken.NewHS256Signer(secret, "https://id.example").SignAccess(claims, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, present := rawClaims(t, signed)["email"]; present {
			t.Error("signer: `email` key is on the wire; omitempty should have dropped it")
		}
	})

	t.Run("scope-less first-party token keeps the address", func(t *testing.T) {
		// /auth/login negotiates no scope; the account center still needs it.
		claims := build("")
		legacy, err := utils.GenerateAccessToken(secret, claims, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		got, err := utils.ParseToken(legacy, secret)
		if err != nil {
			t.Fatal(err)
		}
		if got.Email != addr {
			t.Errorf("password-login token: Email = %q, want %q", got.Email, addr)
		}
	})
}
