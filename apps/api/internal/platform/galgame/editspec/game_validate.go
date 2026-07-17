package editspec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"api/internal/platform/galgame/model"
)

// Validators for galgame.game field values. Posture (03 号裁定 1): mirror the
// old write face — which was permissive — instead of inventing vocabulary.
// Values arrive as decoded JSON (`string`, `float64`, `bool`, `[]any`,
// `map[string]any`, `nil`), both from wire patches and from LoadSnapshot
// round-trips, so every validator speaks those shapes.

// introMaxRunes is a sanity ceiling only — the old face never bounded intro
// length (text column); this guards against absurd payloads, nothing more.
const introMaxRunes = 100000

var gameVNDBIDRegex = regexp.MustCompile(`^v\d+$`)

func validateVNDBID(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	if s != "" && !gameVNDBIDRegex.MatchString(s) {
		return fmt.Errorf("must be empty or a canonical VNDB id (v123)")
	}
	return nil
}

// validateStringMax accepts any string up to max runes (empty allowed — the
// old face treated empty as a legitimate authoritative value).
func validateStringMax(max int) func(any) error {
	return func(v any) error {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}
		if len([]rune(s)) > max {
			return fmt.Errorf("must be at most %d characters", max)
		}
		return nil
	}
}

// validateShortString: any string ≤32 runes, EMPTY INCLUDED. Used for
// original_language / age_limit — the old face enforced no vocabulary and its
// overlay semantics allowed an authoritative empty replacement, so the mirror
// must too (permissive-mirror, don't invent; a PR built from a sparse
// snapshot legitimately proposes "" for these).
func validateShortString(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	if len([]rune(s)) > 32 {
		return fmt.Errorf("must be at most 32 characters")
	}
	return nil
}

func validateContentLimit(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	// "" tolerated for the same permissive-mirror reason as validateShortString.
	if s != "" && s != "sfw" && s != "nsfw" {
		return fmt.Errorf("must be sfw or nsfw")
	}
	return nil
}

func validateReleaseDate(v any) error {
	if v == nil {
		return nil // unknown date
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be null or a string")
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("must be YYYY-MM-DD")
	}
	return nil
}

func validateBool(v any) error {
	if _, ok := v.(bool); !ok {
		return fmt.Errorf("must be a boolean")
	}
	return nil
}

// validateReleasePrecision accepts the typed vocabulary plus "" — pre-P3
// snapshots legitimately carry the empty value (Apply maps it to "unknown",
// mirroring ApplySnapshot's ResolveReleasePrecision fallback).
func validateReleasePrecision(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("must be a string")
	}
	switch s {
	case "", "day", "month", "year", "tba", "unknown":
		return nil
	}
	return fmt.Errorf("must be one of day/month/year/tba/unknown")
}

// validateNullableInt accepts null or a whole number ≥ min.
func validateNullableInt(min int64) func(any) error {
	return func(v any) error {
		if v == nil {
			return nil
		}
		n, err := wholeNumber(v)
		if err != nil {
			return err
		}
		if n < min {
			return fmt.Errorf("must be ≥ %d", min)
		}
		return nil
	}
}

func validateStatus(v any) error {
	n, err := wholeNumber(v)
	if err != nil {
		return err
	}
	if n < 0 || n > 4 {
		return fmt.Errorf("must be 0..4")
	}
	return nil
}

func validateAliases(v any) error {
	items, err := decodeList[string](v, false)
	if err != nil {
		return fmt.Errorf("must be an array of strings")
	}
	if len(items) > 200 {
		return fmt.Errorf("at most 200 aliases")
	}
	for i, s := range items {
		if s == "" {
			return fmt.Errorf("alias %d must not be empty", i)
		}
		if len([]rune(s)) > 500 {
			return fmt.Errorf("alias %d must be at most 500 characters", i)
		}
	}
	return nil
}

func validateIDList(v any) error {
	ids, err := decodeList[int](v, false)
	if err != nil {
		return fmt.Errorf("must be an array of integers")
	}
	if len(ids) > 500 {
		return fmt.Errorf("at most 500 ids")
	}
	for i, id := range ids {
		if id < 1 {
			return fmt.Errorf("id %d must be ≥ 1", i)
		}
	}
	return nil
}

func validateLinks(v any) error {
	links, err := decodeList[model.SnapshotLink](v, true)
	if err != nil {
		return fmt.Errorf("must be an array of {name,link,source,source_key}: %v", err)
	}
	for i, l := range links {
		if l.Name == "" || l.Link == "" {
			return fmt.Errorf("link %d needs a non-empty name and link", i)
		}
		if len([]rune(l.Name)) > 1000 || len([]rune(l.Link)) > 1000 {
			return fmt.Errorf("link %d name/link must be at most 1000 characters", i)
		}
	}
	return nil
}

func validateCovers(v any) error {
	covers, err := decodeList[model.SnapshotCover](v, true)
	if err != nil {
		return fmt.Errorf("must be an array of cover objects: %v", err)
	}
	pinned := 0
	for i, c := range covers {
		if c.ImageHash == "" {
			return fmt.Errorf("cover %d needs an image_hash", i)
		}
		if c.SortOrder < 0 {
			return fmt.Errorf("cover %d sort_order must be ≥ 0", i)
		}
		if c.SortOrder == 0 {
			pinned++
		}
	}
	if pinned > 1 {
		return fmt.Errorf("at most one cover may be pinned (sort_order=0)")
	}
	return nil
}

func validateScreenshots(v any) error {
	shots, err := decodeList[model.SnapshotScreenshot](v, true)
	if err != nil {
		return fmt.Errorf("must be an array of screenshot objects: %v", err)
	}
	for i, sh := range shots {
		if sh.ImageHash == "" {
			return fmt.Errorf("screenshot %d needs an image_hash", i)
		}
		if sh.SortOrder < 0 {
			return fmt.Errorf("screenshot %d sort_order must be ≥ 0", i)
		}
	}
	return nil
}

// wholeNumber lifts a decoded-JSON number (float64; int/int64 defensively)
// into an int64, rejecting fractions.
func wholeNumber(v any) (int64, error) {
	switch n := v.(type) {
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("must be an integer")
		}
		return int64(n), nil
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	}
	return 0, fmt.Errorf("must be a number")
}

// decodeList re-marshals a decoded-JSON array into a typed slice. strict
// enables DisallowUnknownFields (the "keys ⊆ struct" contract for object
// lists). A null value decodes as the EMPTY list: pre-2025 revision
// snapshots legitimately store null for list fields (nil Go slices marshaled
// before TakeSnapshot grew its make()-empty discipline), and the old
// ApplySnapshot treated nil as "clear the relation" — a revert onto such a
// snapshot must keep working (E2a rehearsal caught a 500 here).
func decodeList[T any](v any, strict bool) ([]T, error) {
	if v == nil {
		return []T{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if strict {
		dec.DisallowUnknownFields()
	}
	var out []T
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
