package devapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"math/big"
	"strings"
)

// Key format (docs/developer-platform/01-design.md §4.1):
//
//	nm_live_<base62(24B)> / nm_test_<base62(24B)>
//
// The `nm` brand + environment prefix lets secret-leak scanners recognise the
// key; base62 keeps it copy-paste and URL clean. 24 random bytes = 192 bits of
// entropy. At rest only sha256(key) is stored ("sha256:<hex>"); the plaintext
// is returned to the admin once at mint and never persisted (mirrors
// site/model HashOAuthClientSecret / VerifySecret).
const (
	// LivePrefix / TestPrefix are the environment prefixes.
	LivePrefix = "nm_live_"
	TestPrefix = "nm_test_"

	keyHashPrefix  = "sha256:"
	keyRandomBytes = 24
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// GenerateKey mints a new plaintext API key with the given environment prefix
// (LivePrefix or TestPrefix). The returned string is the only time the key
// appears in plaintext.
func GenerateKey(prefix string) (string, error) {
	b := make([]byte, keyRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base62Encode(b), nil
}

// HashKey returns the at-rest form of a key: "sha256:<hex(sha256(key))>". Keys
// are high-entropy random, so a fast cryptographic hash is sufficient (as with
// OAuth client secrets) and keeps per-request auth cheap.
func HashKey(plaintext string) string {
	return keyHashPrefix + hashHex(plaintext)
}

// hashHex returns the bare hex of sha256(plaintext) — the credential cache key
// body (`devkey:<hashHex>`), never the key itself.
func hashHex(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// VerifyKeyHash constant-time compares a presented plaintext key against a
// stored "sha256:<hex>" value. Belt-and-suspenders over the indexed hash lookup
// (defence in depth, mirroring VerifySecret's constant-time compare).
func VerifyKeyHash(presented, storedHash string) bool {
	h, ok := strings.CutPrefix(storedHash, keyHashPrefix)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashHex(presented)), []byte(h)) == 1
}

// KeyMetadata derives the non-secret identifiers stored alongside the hash: the
// prefix (env prefix + first 4 body chars, e.g. "nm_live_a1b2") and the last 4
// chars, both used by the portal to identify a key without revealing it.
func KeyMetadata(plaintext string) (prefix, last4 string) {
	env, body := "", plaintext
	switch {
	case strings.HasPrefix(plaintext, LivePrefix):
		env, body = LivePrefix, plaintext[len(LivePrefix):]
	case strings.HasPrefix(plaintext, TestPrefix):
		env, body = TestPrefix, plaintext[len(TestPrefix):]
	}
	prefix = env
	if len(body) >= 4 {
		prefix += body[:4]
	} else {
		prefix += body
	}
	last4 = plaintext
	if len(plaintext) >= 4 {
		last4 = plaintext[len(plaintext)-4:]
	}
	return prefix, last4
}

// HasKeyPrefix reports whether raw looks like one of our API keys (nm_live_ /
// nm_test_). A non-matching bearer value (e.g. a JWT) is not our credential.
func HasKeyPrefix(raw string) bool {
	return strings.HasPrefix(raw, LivePrefix) || strings.HasPrefix(raw, TestPrefix)
}

// base62Encode renders bytes as a big-endian base62 string. Leading zero bytes
// collapse (opaque token — full 192-bit entropy is preserved in the value).
func base62Encode(b []byte) string {
	n := new(big.Int).SetBytes(b)
	if n.Sign() == 0 {
		return "0"
	}
	base := big.NewInt(62)
	mod := new(big.Int)
	buf := make([]byte, 0, 33)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		buf = append(buf, base62Alphabet[mod.Int64()])
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
