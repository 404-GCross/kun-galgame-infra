package bangumiwiki

import (
	"errors"
	"fmt"
	"strings"
)

// In-house infobox parser. Written black-box from bangumi/wiki-syntax-spec
// plus full-dump differential testing against the reference implementation
// (github.com/bangumi/wiki-parser-go v0.0.2) — bug-for-bug compatible on the
// 2026-06-30 dump (zero divergence over ~958k infoboxes; see testdata/golden
// and the step-08 execution report). Deliberately conservative: quirks of
// the reference behavior are preserved, "more spec-correct" would be a
// regression for Silver-layer stability.

// errSyntax is the root of every parse error.
var errSyntax = errors.New("invalid wiki syntax")

// asciiCutset is what the reference implementation trims: ASCII whitespace
// ONLY. Unicode spaces (U+00A0 no-break space, U+3000 ideographic space, ...)
// are part of keys/values — strings.TrimSpace would eat them and diverge
// (caught by the full-dump diff: 414 rows).
const asciiCutset = " \t\r\n"

func trimASCII(s string) string {
	return strings.Trim(s, asciiCutset)
}

func syntaxErr(lino int, line, msg string) error {
	return fmt.Errorf("%w: %s (line %d: %q)", errSyntax, msg, lino, line)
}

// parseInfobox parses one "{{Infobox ...}}" block into the mirror types.
// Empty / whitespace-only input yields a zero Infobox with no error
// (reference behavior, pinned in TestParse_EmptyInput).
func parseInfobox(input string) (Infobox, error) {
	s := trimASCII(input)
	if s == "" {
		return Infobox{}, nil
	}
	if !strings.HasPrefix(s, "{{Infobox") {
		return Infobox{}, fmt.Errorf("%w: missing '{{Infobox' prefix", errSyntax)
	}
	if !strings.HasSuffix(s, "}}") {
		return Infobox{}, fmt.Errorf("%w: missing '}}' suffix", errSyntax)
	}

	lines := strings.Split(s, "\n")

	// The type is the remainder of the first line; on a single-line infobox
	// the closing braces are part of that line.
	typeLine := strings.TrimPrefix(lines[0], "{{Infobox")
	if len(lines) == 1 {
		typeLine = strings.TrimSuffix(typeLine, "}}")
	}
	box := Infobox{Type: trimASCII(typeLine)}

	// One line ("{{Infobox X}}") or two lines ("{{Infobox X\n}}") carry no
	// fields by definition.
	if len(lines) <= 2 {
		return box, nil
	}

	// Content lines are everything between the first line and the final
	// "}}" line.
	content := lines[1 : len(lines)-1]

	var (
		inArray bool
		current Field // the array field being accumulated
	)
	for i, raw := range content {
		line := trimASCII(raw)
		lino := i + 2 // 1-based, counting the "{{Infobox" line

		if inArray {
			switch {
			case line == "":
				continue
			case line == "}":
				box.Fields = append(box.Fields, current)
				inArray = false
				current = Field{}
			case strings.HasPrefix(line, "["):
				if !strings.HasSuffix(line, "]") {
					return Infobox{}, syntaxErr(lino, raw, "array item must be wrapped in '[]'")
				}
				body := line[1 : len(line)-1]
				item := Item{}
				if k, v, found := strings.Cut(body, "|"); found {
					item.Key = trimASCII(k)
					item.Value = trimASCII(v)
				} else {
					item.Value = trimASCII(body)
				}
				current.Items = append(current.Items, item)
			default:
				return Infobox{}, syntaxErr(lino, raw, "array is not closed by '}'")
			}
			continue
		}

		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			return Infobox{}, syntaxErr(lino, raw, "expected '|' to start a new field")
		}
		key, value, found := strings.Cut(line[1:], "=")
		if !found {
			return Infobox{}, syntaxErr(lino, raw, "expected '=' after the field name")
		}
		key = trimASCII(key)
		value = trimASCII(value)
		if value == "{" {
			inArray = true
			current = Field{Key: key, Array: true}
			continue
		}
		box.Fields = append(box.Fields, Field{Key: key, Value: value, Null: value == ""})
	}
	if inArray {
		return Infobox{}, fmt.Errorf("%w: array field %q is not closed by '}'", errSyntax, current.Key)
	}
	return box, nil
}
