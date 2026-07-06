// Package bangumiwiki is the single entry point for parsing Bangumi wiki
// infobox text. Ingest pipelines import this package rather than the upstream
// github.com/bangumi/wiki-parser-go, so the upstream dependency (and any
// future swap of it) stays contained here.
package bangumiwiki

import (
	"fmt"

	wiki "github.com/bangumi/wiki-parser-go"
)

// Infobox is a parsed "{{Infobox ...}}" block. It mirrors the upstream
// parser's output one-to-one.
type Infobox struct {
	// Type is the template name following "{{Infobox", e.g. "Game".
	Type   string
	Fields []Field
}

// Field is one "|key=..." entry. Scalar fields carry Value; array fields
// ("|key={ [item] ... }") carry Items with Array set.
type Field struct {
	Key   string
	Value string
	Items []Item
	Array bool
	// Null is true when the field is present but empty.
	Null bool
}

// Item is one "[value]" or "[key|value]" entry of an array field.
type Item struct {
	Key   string
	Value string
}

// Parse parses infobox source text. It never panics: malformed input comes
// back as an error. Following the upstream parser, empty and whitespace-only
// input is not an error — it yields a zero Infobox.
func Parse(infobox string) (box Infobox, err error) {
	// The upstream parser is trusted not to panic on well-formed grammar, but
	// it processes arbitrary user-authored wiki text; a panic here must
	// surface as parse_error data, never crash an ingest batch.
	defer func() {
		if r := recover(); r != nil {
			box = Infobox{}
			err = fmt.Errorf("bangumiwiki: parser panic: %v", r)
		}
	}()

	w, perr := wiki.Parse(infobox)
	if perr != nil {
		return Infobox{}, fmt.Errorf("bangumiwiki: %w", perr)
	}
	return fromUpstream(w), nil
}

// fromUpstream copies the upstream result into this package's types so the
// upstream library never leaks into caller signatures.
func fromUpstream(w wiki.Wiki) Infobox {
	box := Infobox{Type: w.Type}
	if len(w.Fields) > 0 {
		box.Fields = make([]Field, 0, len(w.Fields))
	}
	for _, f := range w.Fields {
		field := Field{Key: f.Key, Value: f.Value, Array: f.Array, Null: f.Null}
		if len(f.Values) > 0 {
			field.Items = make([]Item, 0, len(f.Values))
			for _, item := range f.Values {
				field.Items = append(field.Items, Item{Key: item.Key, Value: item.Value})
			}
		}
		box.Fields = append(box.Fields, field)
	}
	return box
}
