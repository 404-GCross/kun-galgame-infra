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

func TestEmailClaimRespectsScope(t *testing.T) {
	const secret = "test-secret"
	const addr = "kun@kungal.com"

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
