package utils

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenClaims represents the claims in a JWT token.
//
// Field naming: `ID` (integer PK in OAuth users table) is the FK
// invariant shared across kungal/moyu/wiki. The `sub` claim carries
// the UUID per OAuth/OIDC spec; consumers can pick either. The legacy
// `uid` claim was renamed to `id` in v0.3.0 — all downstream verifiers
// must read `id` and stop reading `uid`.
type TokenClaims struct {
	UserUUID string   `json:"sub"`
	ID       uint     `json:"id"`
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Scope    string   `json:"scope,omitempty"`
	SiteID   uint     `json:"site_id,omitempty"`
	Role     int      `json:"role,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	// ClientID is the OAuth client the token was issued to (RFC 9068 §2.2).
	// Empty for first-party /auth/login tokens, which have no client.
	ClientID string `json:"client_id,omitempty"`
	jwt.RegisteredClaims
}

// GenerateAccessToken generates a new access token
func GenerateAccessToken(secret string, claims TokenClaims, expiry time.Duration) (string, error) {
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", err
	}

	claims.RegisteredClaims = jwt.RegisteredClaims{
		ID:        hex.EncodeToString(jtiBytes),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		NotBefore: jwt.NewNumericDate(time.Now()),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GenerateOpaqueRefreshToken returns a high-entropy opaque refresh token (32
// random bytes, base64url). Refresh tokens are matched by DB value on both
// refresh paths (never signature-verified — ParseRefreshToken has no callers),
// so an opaque string is the correct, spec-clean form. Old JWT refresh tokens
// keep working because the lookup is by value, not format.
// Design: docs/auth/03-oidc-standardization-design.md §5.3.
func GenerateOpaqueRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateRefreshToken generates a new refresh token.
// Each token includes a unique jti (JWT ID) to prevent collisions
// when multiple tokens are issued for the same user within the same second.
//
// Deprecated: refresh tokens are DB-looked-up, not signature-verified; use
// GenerateOpaqueRefreshToken. Kept only until all mint sites migrate.
func GenerateRefreshToken(secret string, userUUID string, expiry time.Duration) (string, error) {
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", err
	}

	claims := jwt.RegisteredClaims{
		ID:        hex.EncodeToString(jtiBytes),
		Subject:   userUUID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiry)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		NotBefore: jwt.NewNumericDate(time.Now()),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken parses and validates a JWT token
func ParseToken(tokenString, secret string) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*TokenClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrSignatureInvalid
}

// ParseRefreshToken parses and validates a refresh token
func ParseRefreshToken(tokenString, secret string) (string, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(*jwt.RegisteredClaims); ok && token.Valid {
		return claims.Subject, nil
	}

	return "", jwt.ErrSignatureInvalid
}
