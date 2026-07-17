package editing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"gorm.io/datatypes"
)

// Delta is one amendment's patch delta: set changes/adds field values,
// unset removes fields from the effective patch (= rejecting that field).
type Delta struct {
	Set   map[string]any `json:"set,omitempty"`
	Unset []string       `json:"unset,omitempty"`
}

// decodeDelta parses an amendment's stored patch_delta.
func decodeDelta(raw datatypes.JSON) (Delta, error) {
	var d Delta
	if err := json.Unmarshal(raw, &d); err != nil {
		return Delta{}, fmt.Errorf("editing: decode delta: %w", err)
	}
	return d, nil
}

// decodeObject parses a stored JSONB key→value document (patch, snapshot).
func decodeObject(raw datatypes.JSON) (map[string]any, error) {
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("editing: decode object: %w", err)
	}
	return m, nil
}

// decodeKeys parses a stored changed_fields array.
func decodeKeys(raw datatypes.JSON) ([]string, error) {
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("editing: decode keys: %w", err)
	}
	return keys, nil
}

// encodeJSON marshals a value into a JSONB column. The inputs are engine-
// built maps/slices of JSON-native values; a marshal failure is a bug.
func encodeJSON(v any) (datatypes.JSON, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("editing: encode json: %w", err)
	}
	return datatypes.JSON(b), nil
}

// effectivePatch computes original patch ⊕ amendments (in seq order) and the
// set of keys any amendment touched (used by rebase: an amended field counts
// as re-adjudicated by a reviewer, so it is no longer a conflict).
func effectivePatch(p *Proposal, amendments []ProposalAmendment) (map[string]any, map[string]struct{}, error) {
	patch, err := decodeObject(p.Patch)
	if err != nil {
		return nil, nil, err
	}
	touched := make(map[string]struct{})
	for i := range amendments {
		d, err := decodeDelta(amendments[i].PatchDelta)
		if err != nil {
			return nil, nil, err
		}
		for k, v := range d.Set {
			patch[k] = v
			touched[k] = struct{}{}
		}
		for _, k := range d.Unset {
			delete(patch, k)
			touched[k] = struct{}{}
		}
	}
	return patch, touched, nil
}

// jsonValueEqual compares two values by their canonical JSON encoding, so a
// float64(2) from a decoded patch equals an int16(2) from a loaded snapshot.
// Map keys marshal sorted, so object encodings are deterministic. Marshal
// errors count as unequal (conservative: the merge then treats the field as
// changed and the validator/apply surface the real problem).
func jsonValueEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// sortedKeys returns a map's keys in stable order (deterministic validation
// order, changed_fields output, and error messages).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
