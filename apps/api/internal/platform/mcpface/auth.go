package mcpface

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// keyPrefix is the shared prefix of every NextMoe API key (nm_live_ / nm_test_).
// The MCP layer only does this FORM check; the REAL authentication is upstream.
const keyPrefix = "nm_"

// devPortalURL is where a caller mints or inspects an API key.
const devPortalURL = "https://developer.nextmoe.dev"

// bearerToken extracts the API key from the request's Authorization header,
// requiring the "Bearer " scheme and one of our nm_ key prefixes. ok=false means
// the key is missing or malformed — a pure shape check, deliberately NOT a
// validity check (design §3): a well-formed but invalid/expired key passes here
// and is rejected by the upstream face, which returns the authoritative 401.
func bearerToken(header http.Header) (token string, ok bool) {
	if header == nil {
		return "", false
	}
	v, found := strings.CutPrefix(header.Get("Authorization"), "Bearer ")
	if !found {
		return "", false
	}
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, keyPrefix) {
		return "", false
	}
	return v, true
}

// keyFingerprint is the first 8 hex chars of the SHA-256 of the raw key — the
// ONLY form of the key that may reach a log line (design §5). It is one-way, so
// a leaked log never yields the key, yet it still correlates a caller's calls.
func keyFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}
