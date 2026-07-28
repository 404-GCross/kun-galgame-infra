// public_cursor.go — the opaque keyset cursor codec for the /v1/catalog
// browse lanes (works list, changes feed; doc 106 W1). FROZEN after W1: the
// wire form is base64url(JSON) and both consumers treat it as opaque — only
// this file may know its shape.
package service

import (
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
)

// ErrBadCursor is returned by the list/changes lanes when a caller-supplied
// cursor does not decode or does not match the request's sort — the handler
// maps it to a 400 (never a 500: a malformed cursor is caller error).
var ErrBadCursor = stderrors.New("catalog: malformed or mismatched cursor")

// publicCursor is the keyset position for the works list and changes feed.
// Sort tells which lane minted it (works list: "id" | "updated"; changes:
// "changes") so a cursor can never be replayed across lanes/sorts.
type publicCursor struct {
	Sort string `json:"s"`
	ID   int64  `json:"id"`
	// Updated is the RFC3339Nano watermark, present on the updated/changes
	// lanes only.
	Updated string `json:"u,omitempty"`
}

// encodePublicCursor renders the position as base64url(JSON) — opaque on the
// wire, versioned by its field set.
func encodePublicCursor(c publicCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodePublicCursor parses a caller cursor and pins it to the expected lane.
// Empty input = first page (zero cursor, no error).
func decodePublicCursor(raw, wantSort string) (publicCursor, error) {
	if raw == "" {
		return publicCursor{Sort: wantSort}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return publicCursor{}, ErrBadCursor
	}
	var c publicCursor
	if err := json.Unmarshal(b, &c); err != nil || c.Sort != wantSort {
		return publicCursor{}, ErrBadCursor
	}
	return c, nil
}
