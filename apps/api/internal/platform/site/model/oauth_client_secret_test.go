package model

import (
	"strings"
	"testing"
)

func TestOAuthClientVerifySecret(t *testing.T) {
	const secret = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef0"

	hashed := &OAuthClient{Secret: HashOAuthClientSecret(secret)}
	if hashed.Secret == secret {
		t.Fatal("secret stored as plaintext, expected a hash")
	}
	if !strings.HasPrefix(hashed.Secret, secretHashPrefix) {
		t.Fatalf("stored secret missing %q scheme prefix: %q", secretHashPrefix, hashed.Secret)
	}
	if !hashed.VerifySecret(secret) {
		t.Fatal("hashed: correct secret rejected")
	}
	if hashed.VerifySecret(secret + "x") {
		t.Fatal("hashed: wrong secret accepted")
	}
	if hashed.VerifySecret("") {
		t.Fatal("hashed: empty secret accepted")
	}

	legacy := &OAuthClient{Secret: secret}
	if !legacy.VerifySecret(secret) {
		t.Fatal("legacy plaintext: correct secret rejected")
	}
	if legacy.VerifySecret("nope") {
		t.Fatal("legacy plaintext: wrong secret accepted")
	}
}
