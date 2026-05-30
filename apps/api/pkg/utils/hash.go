package utils

import (
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/matthewhartstonge/argon2"
	stdArgon2 "golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// HashPassword hashes a password using argon2 (new system standard format)
func HashPassword(password string) (string, error) {
	cfg := argon2.DefaultConfig()
	encoded, err := cfg.HashEncoded([]byte(password))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// VerifyPassword verifies a password against a new system argon2 hash
func VerifyPassword(password, hash string) (bool, error) {
	ok, err := argon2.VerifyEncoded([]byte(password), []byte(hash))
	if err != nil {
		return false, err
	}
	return ok, nil
}

// VerifyBcryptPassword verifies a password against a kungal-nuxt bcrypt hash ($2b$07$...)
func VerifyBcryptPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// VerifyMoyuPassword verifies a password against a moyu-nextjs custom argon2id hash.
// Format: "salt_hex:hash_hex"
// Params: argon2id, t=2, m=8192, p=3, keyLen=32
//
// Note: @noble/hashes passes the hex salt string directly as bytes to argon2
// (i.e., the 32-char hex string is used as 32 raw bytes, NOT hex-decoded to 16 bytes).
func VerifyMoyuPassword(password, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}

	// Salt is used as raw string bytes (how @noble/hashes handles string salt)
	salt := []byte(parts[0])

	expectedHash, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	// Reproduce the same argon2id computation as @noble/hashes
	computed := stdArgon2.IDKey([]byte(password), salt, 2, 8192, 3, uint32(len(expectedHash)))

	// Constant-time comparison (crypto/subtle, not a hand-rolled loop the
	// compiler could short-circuit). ConstantTimeCompare also returns 0 on a
	// length mismatch, so no separate length check is needed.
	return subtle.ConstantTimeCompare(computed, expectedHash) == 1
}
