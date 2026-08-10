package oidckeys

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
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

type KeyMaterial struct {
	Kid        string
	Alg        string
	PublicJWK  map[string]string
	PrivateDER []byte
}

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
	ep, err := pub.ECDH()
	if err != nil {
		return nil, err
	}
	raw := ep.Bytes()
	byteLen := (len(raw) - 1) / 2
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

func ParsePrivate(der []byte) (any, error) {
	return x509.ParsePKCS8PrivateKey(der)
}

func PublicKeyFromJWK(jwk map[string]any) (crypto.PublicKey, error) {
	kty, _ := jwk["kty"].(string)
	switch kty {
	case "EC":
		if crv, _ := jwk["crv"].(string); crv != "P-256" {
			return nil, fmt.Errorf("oidckeys: unsupported EC crv %q", crv)
		}
		xs, _ := jwk["x"].(string)
		ys, _ := jwk["y"].(string)
		x, err := base64.RawURLEncoding.DecodeString(xs)
		if err != nil {
			return nil, fmt.Errorf("oidckeys: bad EC x: %w", err)
		}
		y, err := base64.RawURLEncoding.DecodeString(ys)
		if err != nil {
			return nil, fmt.Errorf("oidckeys: bad EC y: %w", err)
		}
		point := make([]byte, 0, 1+len(x)+len(y))
		point = append(point, 0x04)
		point = append(point, x...)
		point = append(point, y...)
		ecdhPub, err := ecdh.P256().NewPublicKey(point)
		if err != nil {
			return nil, fmt.Errorf("oidckeys: bad EC point: %w", err)
		}
		der, err := x509.MarshalPKIXPublicKey(ecdhPub)
		if err != nil {
			return nil, err
		}
		return x509.ParsePKIXPublicKey(der)
	case "RSA":
		ns, _ := jwk["n"].(string)
		es, _ := jwk["e"].(string)
		n, err := base64.RawURLEncoding.DecodeString(ns)
		if err != nil {
			return nil, fmt.Errorf("oidckeys: bad RSA n: %w", err)
		}
		e, err := base64.RawURLEncoding.DecodeString(es)
		if err != nil {
			return nil, fmt.Errorf("oidckeys: bad RSA e: %w", err)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}, nil
	default:
		return nil, fmt.Errorf("oidckeys: unsupported kty %q", kty)
	}
}

func ParseJWKSet(raw []byte) (map[string]crypto.PublicKey, error) {
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	out := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, jwk := range set.Keys {
		kid, _ := jwk["kid"].(string)
		if kid == "" {
			continue
		}
		pub, err := PublicKeyFromJWK(jwk)
		if err != nil {
			continue
		}
		out[kid] = pub
	}
	return out, nil
}

func DeriveKEK(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

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
