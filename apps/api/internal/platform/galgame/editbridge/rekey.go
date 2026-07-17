package editbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"api/internal/platform/galgame/editspec"

	"gorm.io/datatypes"
)

// Key renaming between the historical snapshot vocabulary and the eternal
// field keys (editspec.OldToNew, 1:1). Values are carried as raw JSON — the
// rename is purely mechanical so migrated snapshots stay byte-faithful
// (modulo jsonb key-order normalization, which no JSON consumer can observe).

// RekeySnapshotOldToNew renames a historical snapshot document's top-level
// keys to eternal field keys. Unknown old keys hard-fail: the surveyed corpus
// contains exactly the registered vocabulary, so an unknown key means the
// mapping table is incomplete — the transform must stop, not guess.
func RekeySnapshotOldToNew(raw []byte) (datatypes.JSON, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("editbridge: decode old snapshot: %w", err)
	}
	out := make(map[string]json.RawMessage, len(doc))
	for k, v := range doc {
		newKey, ok := editspec.OldToNew[k]
		if !ok {
			return nil, fmt.Errorf("editbridge: old snapshot key %q has no eternal-key mapping", k)
		}
		out[newKey] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(b), nil
}

// RekeySnapshotNewToOld renames an engine snapshot back to the old wire
// vocabulary. galgame.game.status is DROPPED (no old-snapshot slot — the old
// struct never carried it); any other unmapped key hard-fails.
func RekeySnapshotNewToOld(raw []byte) (datatypes.JSON, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("editbridge: decode engine snapshot: %w", err)
	}
	out := make(map[string]json.RawMessage, len(doc))
	for k, v := range doc {
		if k == editspec.FieldStatus {
			continue
		}
		oldKey, ok := editspec.NewToOld[k]
		if !ok {
			return nil, fmt.Errorf("editbridge: engine snapshot key %q has no old-wire mapping", k)
		}
		out[oldKey] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(b), nil
}

// RekeyKeysOldToNew maps a changed_fields list old→new, preserving order.
func RekeyKeysOldToNew(keys []string) ([]string, error) {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		newKey, ok := editspec.OldToNew[k]
		if !ok {
			return nil, fmt.Errorf("editbridge: changed_fields key %q has no eternal-key mapping", k)
		}
		out = append(out, newKey)
	}
	return out, nil
}

// RekeyKeysNewToOld maps a changed_fields list new→old (status included —
// "status" is a legitimate new-era changed_fields label on the old wire even
// though it was never a snapshot key). Unknown keys pass through unchanged
// (defensive: a future field key must not break history rendering).
func RekeyKeysNewToOld(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if oldKey, ok := editspec.NewToOld[k]; ok {
			out = append(out, oldKey)
		} else {
			out = append(out, k)
		}
	}
	return out
}

// EncodeNote packs the old PR title/message pair into a proposal note. The
// encoding is a TOTAL bijection: note = title + "\n\n" + message, always —
// title is a single-line input by contract (surveyed: no historical title
// contains a newline), so the FIRST "\n\n" is an unambiguous separator, and
// an empty title yields a leading "\n\n" marker.
func EncodeNote(title, message string) string {
	return title + "\n\n" + message
}

// DecodeNote is EncodeNote's inverse. It also degrades gracefully on plain
// single-string notes (revert / admin-reason / link notes written by the
// adapter's non-PR paths): no separator → the whole note is the title slot.
func DecodeNote(note string) (title, message string) {
	if strings.HasPrefix(note, "\n\n") {
		return "", note[2:]
	}
	if i := strings.Index(note, "\n\n"); i >= 0 {
		return note[:i], note[i+2:]
	}
	return note, ""
}

// prMergeNote reproduces the old merged-revision note composition from a
// title/message pair (revision_service.prMergeNote):
// both → title\n\nmessage; title-only → title; else message.
func prMergeNote(title, message string) string {
	switch {
	case title != "" && message != "":
		return title + "\n\n" + message
	case title != "":
		return title
	default:
		return message
	}
}

// wireNoteFromProposal renders a proposal's stored note for the old wire:
// decode the (possibly EncodeNote-packed) note and recompose in prMergeNote
// form. For plain single-string notes this is the identity.
func wireNoteFromProposal(note string) string {
	title, message := DecodeNote(note)
	return prMergeNote(title, message)
}
