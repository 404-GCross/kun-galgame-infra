package bangumiwiki

import (
	"errors"
	"fmt"
	"strings"
)

var errSyntax = errors.New("invalid wiki syntax")

const asciiCutset = " \t\r\n"

func trimASCII(s string) string {
	return strings.Trim(s, asciiCutset)
}

func syntaxErr(lino int, line, msg string) error {
	return fmt.Errorf("%w: %s (line %d: %q)", errSyntax, msg, lino, line)
}

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

	typeLine := strings.TrimPrefix(lines[0], "{{Infobox")
	if len(lines) == 1 {
		typeLine = strings.TrimSuffix(typeLine, "}}")
	}
	box := Infobox{Type: trimASCII(typeLine)}

	if len(lines) <= 2 {
		return box, nil
	}

	content := lines[1 : len(lines)-1]

	var (
		inArray bool
		current Field
	)
	for i, raw := range content {
		line := trimASCII(raw)
		lino := i + 2

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
