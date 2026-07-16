package service

import (
	"encoding/json"
	"strings"
)

// Query-flexibility layer for the open-API galgame public face (step 07):
// list-level include expansion (officials,scores) + a Google-style sparse
// fieldset (fields=). Both are ADD-ONLY over the frozen step-02 contract — every
// new parameter is optional and the default response is byte-for-byte unchanged
// (the oasdiff gate is the hard acceptance check).

// PublicItemInclude selects the optional blocks a list/batch(brief)/search item
// gains. The list-level include vocabulary is deliberately narrower than the
// detail vocabulary (裁定 1): only officials + scores (the two lightweight
// cross-references); the heavy blocks (intro/covers/taxonomy) stay detail-only.
type PublicItemInclude struct {
	Officials bool
	Scores    bool
}

// ParsePublicItemInclude resolves the comma-separated list-level `include` token
// set. Unknown tokens are ignored (forward-compatible; a client that names a
// future block is never rejected).
func ParsePublicItemInclude(raw string) PublicItemInclude {
	var inc PublicItemInclude
	for _, tok := range strings.Split(raw, ",") {
		switch strings.TrimSpace(tok) {
		case "officials":
			inc.Officials = true
		case "scores":
			inc.Scores = true
		}
	}
	return inc
}

// Any reports whether at least one block was requested (skip the batch loaders
// entirely otherwise).
func (i PublicItemInclude) Any() bool { return i.Officials || i.Scores }

// PublicFields is the parsed sparse-fieldset selector (fields=). Inactive (the
// parameter absent/empty) means "return the full frozen shape". A present set
// trims the response to the named top-level keys — id is always retained (the
// anchor), unknown names are silently ignored, and order is irrelevant (a set).
type PublicFields struct {
	keys   map[string]struct{}
	active bool
}

// ParsePublicFields resolves the comma-separated `fields` CSV. An empty/whitespace
// value yields an inactive selector (no filtering).
func ParsePublicFields(raw string) PublicFields {
	keys := make(map[string]struct{})
	for _, tok := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(tok); t != "" {
			keys[t] = struct{}{}
		}
	}
	return PublicFields{keys: keys, active: len(keys) > 0}
}

// Active reports whether the selector filters at all. When false, callers must
// return the typed DTO untouched so the default response stays byte-identical.
func (f PublicFields) Active() bool { return f.active }

// ApplyPublicFields projects v to the selected top-level keys. It marshals the
// frozen DTO and keeps only the requested keys' RAW bytes, so a retained key is
// byte-for-byte its frozen form ("只裁不改": trim keys, never reshape a value).
//   - id is always retained, whether or not it was named;
//   - a requested key that is absent from the marshaled record (unknown name, or
//     a block the caller did not include) is silently skipped — the key simply
//     does not appear (双向兼容: unknown names never 400);
//   - an inactive selector returns v unchanged (byte-identical default).
//
// Marshal of these plain DTOs cannot fail in practice; on the impossible error
// it returns the full record rather than null (never a silent data loss).
func ApplyPublicFields(v any, f PublicFields) any {
	if !f.active {
		return v
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(b, &full); err != nil {
		return v
	}
	out := make(map[string]json.RawMessage, len(f.keys)+1)
	if raw, ok := full["id"]; ok {
		out["id"] = raw
	}
	for k := range full {
		if _, want := f.keys[k]; want {
			out[k] = full[k]
		}
	}
	return out
}
