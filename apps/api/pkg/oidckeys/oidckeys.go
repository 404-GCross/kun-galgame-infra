// Package oidckeys provides the low-level cryptography for the OIDC OP's
// signing keys: keypair generation (ES256 / RS256), JWK (RFC 7517) public
// encoding with RFC 7638 thumbprint key ids, and AES-256-GCM encryption of
// private keys at rest. It holds NO database or config coupling — the
// signing-key service (internal/platform/auth) owns lifecycle + storage.
//
// Design: docs/auth/03-oidc-standardization-design.md §4.
package oidckeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

const (
	AlgES256 = "ES256"
	AlgRS256 = "RS256"
	rsaBits  = 2048
)

// KeyMaterial is a freshly generated signing key: its public JWK (ready to
// publish in a JWK Set), the PKCS#8-DER private key (to be encrypted at rest),
// and the RFC 7638 thumbprint used as the `kid`.
type KeyMaterial struct {
	Kid        string
	Alg        string
	PublicJWK  map[string]string
	PrivateDER []byte
}

// Generate creates a new signing keypair for the given alg (ES256 or RS256).
// The returned PublicJWK already carries use/alg/kid and is safe to serve in a
// JWK Set; PrivateDER is raw PKCS#8 and MUST be encrypted before storage.
func Generate(alg string) (*KeyMaterial, error) {
	switch alg {
	case AlgES256:
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		jwk, err := ecPublicJWK(&priv.PublicKey)
		if err != nil {
			return nil, err
		}
		return finalize(alg, jwk, der)
	case AlgRS256:
		priv, err := rsa.GenerateKey(rand.Reader, rsaBits)
		if err != nil {
			return nil, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		return finalize(alg, rsaPublicJWK(&priv.PublicKey), der)
	default:
		return nil, fmt.Errorf("oidckeys: unsupported alg %q", alg)
	}
}

func finalize(alg string, jwk map[string]string, der []byte) (*KeyMaterial, error) {
	kid, err := thumbprint(jwk)
	if err != nil {
		return nil, err
	}
	jwk["use"] = "sig"
	jwk["alg"] = alg
	jwk["kid"] = kid
	return &KeyMaterial{Kid: kid, Alg: alg, PublicJWK: jwk, PrivateDER: der}, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func ecPublicJWK(pub *ecdsa.PublicKey) (map[string]string, error) {
	// Use the ecdh view to read the raw point (0x04 || X || Y, uncompressed)
	// instead of the deprecated pub.X / pub.Y big.Int coordinates.
	ep, err := pub.ECDH()
	if err != nil {
		return nil, err
	}
	raw := ep.Bytes()
	byteLen := (len(raw) - 1) / 2 // 32 for P-256
	x := raw[1 : 1+byteLen]
	y := raw[1+byteLen:]
	return map[string]string{"kty": "EC", "crv": "P-256", "x": b64(x), "y": b64(y)}, nil
}

func rsaPublicJWK(pub *rsa.PublicKey) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"n":   b64(pub.N.Bytes()),
		"e":   b64(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// thumbprint computes the RFC 7638 JWK thumbprint (SHA-256, base64url) over the
// required members only. Go's json.Marshal emits map keys in lexicographic
// order, which is exactly RFC 7638's required member ordering.
func thumbprint(jwk map[string]string) (string, error) {
	var req map[string]string
	switch jwk["kty"] {
	case "EC":
		req = map[string]string{"crv": jwk["crv"], "kty": "EC", "x": jwk["x"], "y": jwk["y"]}
	case "RSA":
		req = map[string]string{"e": jwk["e"], "kty": "RSA", "n": jwk["n"]}
	default:
		return "", fmt.Errorf("oidckeys: cannot thumbprint kty %q", jwk["kty"])
	}
	canon, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return b64(sum[:]), nil
}

// ParsePrivate parses a PKCS#8-DER private key into the concrete key type that
// golang-jwt's ES256/RS256 signing methods expect (*ecdsa.PrivateKey /
// *rsa.PrivateKey).
func ParsePrivate(der []byte) (any, error) {
	return x509.ParsePKCS8PrivateKey(der)
}

// --- AES-256-GCM at-rest encryption for private keys (KEK from config) ---

// DeriveKEK turns an arbitrary-length secret string into a fixed 32-byte
// AES-256 key via SHA-256, so the operator can supply any high-entropy
// passphrase for KUN_OIDC_KEY_ENC_KEY.
func DeriveKEK(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Encrypt seals plaintext with AES-256-GCM; the random nonce is prepended to
// the ciphertext (nonce || ciphertext).
func Encrypt(kek, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. Fails (non-nil error) on a wrong KEK or tampering.
func Decrypt(kek, data []byte) ([]byte, error) {
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("oidckeys: ciphertext too short")
	}
	return gcm.Open(nil, data[:ns], data[ns:], nil)
}

func newGCM(kek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
