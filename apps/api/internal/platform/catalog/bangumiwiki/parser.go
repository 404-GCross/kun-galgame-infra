// Package bangumiwiki is the single entry point for parsing Bangumi wiki
// infobox text. The parser is an IN-HOUSE implementation (parse.go), written
// black-box from bangumi/wiki-syntax-spec and verified bug-for-bug compatible
// with the reference github.com/bangumi/wiki-parser-go v0.0.2 by full-dump
// differential testing (2026-06-30 dump, zero divergence). Behavior is
// permanently pinned by two regression suites: the wiki-syntax-spec snapshot
// (testdata/spec) and dump-sampled golden cases (testdata/golden).
//
// The former AGPL dependency is gone as of step 08 — this package may be
// imported by cmd/* binaries.
package bangumiwiki

import "fmt"

// Infobox is a parsed "{{Infobox ...}}" block. It mirrors the reference
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
// back as an error. Following the reference parser, empty and
// whitespace-only input is not an error — it yields a zero Infobox.
func Parse(infobox string) (box Infobox, err error) {
	// The parser handles arbitrary user-authored wiki text; a panic must
	// surface as parse_error data, never crash an ingest batch.
	defer func() {
		if r := recover(); r != nil {
			box = Infobox{}
			err = fmt.Errorf("bangumiwiki: parser panic: %v", r)
		}
	}()

	box, perr := parseInfobox(infobox)
	if perr != nil {
		return Infobox{}, fmt.Errorf("bangumiwiki: %w", perr)
	}
	return box, nil
}
