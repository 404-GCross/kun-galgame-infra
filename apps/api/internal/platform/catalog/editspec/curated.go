package editspec

import (
	"fmt"
	"strings"
)

const curatedSourceID int16 = 12

const curatedMatchedBy = "curated"

const (
	maxListElements = 200
	maxHashRunes    = 128
	maxCaptionRunes = 500
	maxURLRunes     = 2000
	maxIntroRunes   = 50000
)

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
