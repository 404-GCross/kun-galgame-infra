package model

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

// Timestamp is a point-in-time value that ALWAYS JSON-serializes as UTC
// RFC3339 with a literal Z — e.g. "2026-05-10T16:24:28Z".
//
// Why not raw time.Time: Go's encoding/json marshals time.Time with its
// stored location's offset (e.g. "2026-05-10T09:24:28-07:00"). The DB
// driver returns timestamps in the process-local timezone (empirically
// -0700 on a PDT host), so raw-model endpoints leaked offset-form
// timestamps whose wall-clock depended on wherever the server happened
// to run — and diverged from the DTO endpoints that format with an
// explicit .UTC(). Timestamp normalizes to UTC at serialization so every
// galgame API timestamp is the same shape regardless of host timezone,
// matching the Date type's treatment of date-only fields.
//
// GORM autoCreateTime / autoUpdateTime work with this type (verified):
// GORM recognizes the underlying time.Time and auto-populates on
// create/update.
type Timestamp time.Time

const timestampLayout = "2006-01-02T15:04:05Z"

// MarshalJSON renders as UTC RFC3339 ("...Z"); zero value → null.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	tt := time.Time(t)
	if tt.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + tt.UTC().Format(timestampLayout) + `"`), nil
}

// UnmarshalJSON accepts RFC3339 (with or without fractional seconds /
// offset) or null. Stored normalized to UTC.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*t = Timestamp(time.Time{})
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("Timestamp.UnmarshalJSON %q: %w", s, err)
	}
	*t = Timestamp(parsed.UTC())
	return nil
}

// Scan implements sql.Scanner — the driver hands a timestamptz back as a
// time.Time (in whatever location the driver uses; serialization
// normalizes to UTC anyway).
func (t *Timestamp) Scan(v any) error {
	if v == nil {
		*t = Timestamp(time.Time{})
		return nil
	}
	tt, ok := v.(time.Time)
	if !ok {
		return fmt.Errorf("Timestamp.Scan: want time.Time, got %T", v)
	}
	*t = Timestamp(tt)
	return nil
}

// Value implements driver.Valuer. Hand the underlying time.Time straight
// to the driver — let PG store it in the timestamptz column (UTC
// internally). Zero value → SQL NULL.
func (t Timestamp) Value() (driver.Value, error) {
	tt := time.Time(t)
	if tt.IsZero() {
		return nil, nil
	}
	return tt, nil
}

// Time returns the underlying time.Time. Convenience for call sites that
// need .Unix() / .Sub() / comparison without an explicit conversion.
func (t Timestamp) Time() time.Time {
	return time.Time(t)
}
