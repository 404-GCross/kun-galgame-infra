package editspec

import (
	"fmt"
	"strings"
)

// curatedSourceID is the source_id every facet row a human edit writes carries
// — the ONE mechanism separating human writes from importer writes on the
// multi-valued facets (03 定案 §0 line 2).
//
// Physically it is source 12, seeded as `galgame_wiki` (seed.go: the
// first-party product lane). The id is deliberately reused rather than a new
// source minted: the wiki body's rows ARE the first-party human lane, the
// mirror steps have already filed 60k+ works' facets under it, and a second id
// would split one lane in two and force every read face to know both. The
// LABEL is renamed to `curated` at the wave-N5 close, when the last mirror step
// retires; the id never moves (03 §1 / §9-3).
//
// Consequence to hold in mind while the two coexist (03 §8-3): until their
// mirror step retires, the daily chain also writes source-12 rows for intros /
// tags / covers / screenshots of CLAIMED works. That is why the editing face
// opens per-facet in the SAME window its mirror step retires — a single
// persistent writer at any moment — and why N1 only registers the spec instead
// of routing users to it.
const curatedSourceID int16 = 12

// curatedMatchedBy stamps catalog_external_ref rows written by a human edit.
// external_ref has no source-lane column that would separate a human row from
// an importer row on the SAME source (a curated VNDB candidate and the vndb
// importer's exact anchor are both source 2), so matched_by is the lane marker
// there, and the links Apply scopes its full replace to exactly this value.
const curatedMatchedBy = "curated"

// Shared limits for the list fields. Deliberately generous: they exist to stop
// a runaway payload, not to express editorial policy (the biggest real work in
// the registry carries 60 tags and 30 screenshots).
const (
	maxListElements = 200
	maxHashRunes    = 128
	maxCaptionRunes = 500
	maxURLRunes     = 2000
	maxIntroRunes   = 50000
)

// asArray unwraps a decoded JSON array field value.
func asArray(v any, what string) ([]any, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of %s", what)
	}
	if len(arr) > maxListElements {
		return nil, fmt.Errorf("must contain at most %d elements", maxListElements)
	}
	return arr, nil
}

// asObject unwraps one array element as an object and rejects unknown keys —
// the same closed-shape discipline titles established, so a snapshot value and
// a submitted patch that mean the same thing encode identically (no-op
// detection and clean revert round-trips depend on it).
func asObject(el any, index int, allowed ...string) (map[string]any, error) {
	obj, ok := el.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("element %d: must be an object", index)
	}
	for key := range obj {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("element %d: unknown key %q", index, key)
		}
	}
	return obj, nil
}

// objInt reads an integer member. JSON numbers decode to float64; a snapshot
// round-trip can hand back int64, so both are accepted.
func objInt(obj map[string]any, key string, index int, required bool) (int64, error) {
	raw, present := obj[key]
	if !present {
		if required {
			return 0, fmt.Errorf("element %d: %s is required", index, key)
		}
		return 0, nil
	}
	switch n := raw.(type) {
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("element %d: %s must be an integer", index, key)
		}
		return int64(n), nil
	case int64:
		return n, nil
	}
	return 0, fmt.Errorf("element %d: %s must be an integer", index, key)
}

// objString reads a string member; empty/whitespace is rejected when required.
func objString(obj map[string]any, key string, index int, required bool, maxRunes int) (string, error) {
	raw, present := obj[key]
	if !present {
		if required {
			return "", fmt.Errorf("element %d: %s is required", index, key)
		}
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("element %d: %s must be a string", index, key)
	}
	if required && strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("element %d: %s must not be empty", index, key)
	}
	if len([]rune(s)) > maxRunes {
		return "", fmt.Errorf("element %d: %s must be at most %d characters", index, key, maxRunes)
	}
	return s, nil
}

// objBool reads an optional boolean member (absent = false).
func objBool(obj map[string]any, key string, index int) (bool, error) {
	raw, present := obj[key]
	if !present {
		return false, nil
	}
	b, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("element %d: %s must be a boolean", index, key)
	}
	return b, nil
}

// asBool passes a validated boolean scalar through to its column.
func asBool(v any) (any, error) {
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("must be a boolean")
	}
	return b, nil
}

func validateBool(v any) error {
	_, err := asBool(v)
	return err
}
