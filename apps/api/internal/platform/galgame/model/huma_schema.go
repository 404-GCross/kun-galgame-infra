package model

import "github.com/danielgtaylor/huma/v2"

// Huma OpenAPI schema hints for the custom time types.
//
// Timestamp / Date have custom MarshalJSON (RFC3339 string / "YYYY-MM-DD", or
// null when zero), but Huma's reflection only sees the named type wrapping
// time.Time and would emit an opaque object schema (→ generated TS
// `Record<string, never>`). Implementing huma.SchemaProvider makes every
// Huma-derived spec type them correctly — matching what the wire actually sends.
// This is the sole coupling from the model package to Huma; it is a schema hint
// only and does not affect runtime marshaling.

// Schema types a Timestamp as a nullable RFC3339 date-time string.
func (Timestamp) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "date-time", Nullable: true}
}

// Schema types a Date as a nullable "YYYY-MM-DD" string.
func (Date) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "date", Nullable: true}
}
