package mcpface

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

const keyPrefix = "nm_"

const devPortalURL = "https://developer.nextmoe.dev"

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

func keyFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:8]
}
