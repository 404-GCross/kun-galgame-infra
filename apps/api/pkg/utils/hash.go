package utils

import (
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"github.com/matthewhartstonge/argon2"
	stdArgon2 "golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	cfg := argon2.DefaultConfig()
	encoded, err := cfg.HashEncoded([]byte(password))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func VerifyPassword(password, hash string) (bool, error) {
	ok, err := argon2.VerifyEncoded([]byte(password), []byte(hash))
	if err != nil {
		return false, err
	}
	return ok, nil
}

func VerifyBcryptPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func VerifyMoyuPassword(password, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}

	salt := []byte(parts[0])

	expectedHash, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	computed := stdArgon2.IDKey([]byte(password), salt, 2, 8192, 3, uint32(len(expectedHash)))

	return subtle.ConstantTimeCompare(computed, expectedHash) == 1
}
