package oidckeys

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGenerateEncryptSignRoundtrip(t *testing.T) {
	kek := DeriveKEK("test-kek-secret")
	cases := []struct {
		alg    string
		method jwt.SigningMethod
	}{
		{AlgES256, jwt.SigningMethodES256},
		{AlgRS256, jwt.SigningMethodRS256},
	}
	for _, tc := range cases {
		km, err := Generate(tc.alg)
		if err != nil {
			t.Fatalf("%s generate: %v", tc.alg, err)
		}
		if km.Kid == "" || km.PublicJWK["kid"] != km.Kid {
			t.Fatalf("%s kid mismatch: %+v", tc.alg, km.PublicJWK)
		}
		if km.PublicJWK["use"] != "sig" || km.PublicJWK["alg"] != tc.alg {
			t.Fatalf("%s jwk meta wrong: %+v", tc.alg, km.PublicJWK)
		}

		enc, err := Encrypt(kek, km.PrivateDER)
		if err != nil {
			t.Fatalf("%s encrypt: %v", tc.alg, err)
		}
		dec, err := Decrypt(kek, enc)
		if err != nil {
			t.Fatalf("%s decrypt: %v", tc.alg, err)
		}
		priv, err := ParsePrivate(dec)
		if err != nil {
			t.Fatalf("%s parse: %v", tc.alg, err)
		}

		tok := jwt.NewWithClaims(tc.method, jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		})
		tok.Header["kid"] = km.Kid
		signed, err := tok.SignedString(priv)
		if err != nil {
			t.Fatalf("%s sign: %v", tc.alg, err)
		}

		var pub any
		switch k := priv.(type) {
		case *ecdsa.PrivateKey:
			pub = &k.PublicKey
		case *rsa.PrivateKey:
			pub = &k.PublicKey
		default:
			t.Fatalf("%s unexpected key type %T", tc.alg, priv)
		}
		if _, err := jwt.Parse(signed, func(*jwt.Token) (any, error) { return pub, nil }); err != nil {
			t.Fatalf("%s verify: %v", tc.alg, err)
		}
	}
}

func TestThumbprintDeterministic(t *testing.T) {
	km, err := Generate(AlgES256)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := thumbprint(km.PublicJWK)
	if err != nil {
		t.Fatal(err)
	}
	if tp != km.Kid {
		t.Fatalf("thumbprint not stable: %s vs %s", tp, km.Kid)
	}
}

func TestDecryptWrongKEKFails(t *testing.T) {
	km, err := Generate(AlgES256)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := Encrypt(DeriveKEK("kek-a"), km.PrivateDER)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(DeriveKEK("kek-b"), enc); err == nil {
		t.Fatal("expected decrypt failure with wrong KEK, got nil")
	}
}
