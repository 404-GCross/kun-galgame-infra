package model

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Date is a date-only value (no clock time, no timezone) that JSON-
// serializes as "YYYY-MM-DD" — matching the documented galgame API
// contract, the revision/PR snapshot serialization (formatReleaseDate),
// and the Meilisearch index (formatGalgameReleaseDate).
//
// Why not raw *time.Time: Go's encoding/json marshals time.Time as
// RFC3339 ("2019-08-16T00:00:00Z"), which (1) violated the YYYY-MM-DD
// contract every doc + the input validator promise, and (2) bolted a
// phantom 00:00:00Z onto a date-only value, inviting timezone-shift
// off-by-one-day bugs when a consumer converts it to a non-UTC zone.
// galgame.release_date is a PG `date` column — there is no meaningful
// time-of-day to carry.
type Date time.Time

const dateLayout = "2006-01-02"

// MarshalJSON renders as "YYYY-MM-DD"; the zero value → null.
func (d Date) MarshalJSON() ([]byte, error) {
	t := time.Time(d)
	if t.IsZero() {
		return []byte("null"), nil
	}
	return []byte(strconv.Quote(t.UTC().Format(dateLayout))), nil
}

// UnmarshalJSON accepts "YYYY-MM-DD" (or null / ""). Writes normally flow
// through the DTO's *string field + validator, but supporting this keeps
// Date round-trippable (e.g. if a model is ever decoded from JSON).
func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*d = Date(time.Time{})
		return nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return fmt.Errorf("Date.UnmarshalJSON %q: %w", s, err)
	}
	*d = Date(t)
	return nil
}

// Scan implements sql.Scanner — the Postgres driver hands a `date` column
// back as a time.Time at UTC midnight.
func (d *Date) Scan(v any) error {
	if v == nil {
		*d = Date(time.Time{})
		return nil
	}
	t, ok := v.(time.Time)
	if !ok {
		return fmt.Errorf("Date.Scan: want time.Time, got %T", v)
	}
	*d = Date(t)
	return nil
}

// Value implements driver.Valuer — write back as a bare "YYYY-MM-DD"
// string so PG stores it in the `date` column without a tz round-trip.
// Zero value → SQL NULL.
func (d Date) Value() (driver.Value, error) {
	t := time.Time(d)
	if t.IsZero() {
		return nil, nil
	}
	return t.Format(dateLayout), nil
}

// Time returns the underlying time.Time (UTC midnight). Convenience for
// call sites that need year/unix/etc. without an explicit conversion.
func (d Date) Time() time.Time {
	return time.Time(d)
}

// NewDate wraps a time.Time as a *Date, normalizing to UTC. Returns nil
// for a nil input — lets callers map a *time.Time straight to *Date.
func NewDate(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	d := Date(t.UTC())
	return &d
}
