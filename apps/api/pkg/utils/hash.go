package utils

import (
	"github.com/matthewhartstonge/argon2"
)

// HashPassword hashes a password using argon2
func HashPassword(password string) (string, error) {
	cfg := argon2.DefaultConfig()
	encoded, err := cfg.HashEncoded([]byte(password))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// VerifyPassword verifies a password against a hash
func VerifyPassword(password, hash string) (bool, error) {
	ok, err := argon2.VerifyEncoded([]byte(password), []byte(hash))
	if err != nil {
		return false, err
	}
	return ok, nil
}
