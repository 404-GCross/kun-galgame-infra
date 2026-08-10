package utils

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type TokenClaims struct {
	UserUUID  string   `json:"sub"`
	ID        uint     `json:"id"`
	Email     string   `json:"email,omitempty"`
	Name      string   `json:"name"`
	Scope     string   `json:"scope,omitempty"`
	SiteID    uint     `json:"site_id,omitempty"`
	Role      int      `json:"role,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	SiteRoles []string `json:"site_roles,omitempty"`
	ClientID  string   `json:"client_id,omitempty"`
	jwt.RegisteredClaims
}

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

func GenerateOpaqueRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

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
