package service

import (
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
)

var ErrBadCursor = stderrors.New("catalog: malformed or mismatched cursor")

type publicCursor struct {
	Sort    string `json:"s"`
	ID      int64  `json:"id"`
	Updated string `json:"u,omitempty"`
	Ord     int64  `json:"o,omitempty"`
}

func encodePublicCursor(c publicCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

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
