package bgmzhnames

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Infobox keys this wave reads. `简体中文名` is a top-level scalar field; the
// rest are ITEM keys inside the `别名` array field.
const (
	keyPrimaryZh = "简体中文名"
	keyAliasList = "别名"
)

// chineseItemKeys are the `别名` item keys that DECLARE their value to be
// Chinese. Only these are collected: an item whose key names another language
// (日文名 / 纯假名 / 罗马字 / 英文名 …) is wrong for this lane by definition, and
// an UNTAGGED item cannot be sorted — see the package doc for the survey that
// ruled the untagged lane out. Both character sets of each key are listed
// because the Bangumi wiki accepts either spelling.
var chineseItemKeys = map[string]bool{
	"中文名": true, "第二中文名": true, "第三中文名": true, "第四中文名": true,
	"简体中文名": true, "簡體中文名": true, "繁体中文名": true, "繁體中文名": true,
	"中文译名": true, "中文譯名": true,
}

// field is one parsed infobox field. A field is either scalar (Value set) or an
// array (Items set) — the Bangumi parser fills exactly one.
type field struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
	Items []item `json:"Items"`
}

type item struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// parseFields unwraps infobox_parsed and returns its Fields array. ok=false is
// the dirty-value guard: the column may hold JSON null, an object without
// Fields, a JSON null Fields, or a SCALAR where an array belongs (the step-81
// charattrs finding). None of those are readable, and none are an error — the
// caller counts them.
func parseFields(raw []byte) (fields []field, ok bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var envelope struct {
		Fields json.RawMessage `json:"Fields"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, false
	}
	if len(envelope.Fields) == 0 {
		return nil, false
	}
	if err := json.Unmarshal(envelope.Fields, &fields); err != nil {
		return nil, false
	}
	// A JSON null unmarshals into a nil slice WITHOUT an error, so the nil check
	// is what separates "no array here" from an empty one: `[]` decodes to a
	// non-nil empty slice and is a legitimately readable (if unsupplied) infobox.
	return fields, fields != nil
}

// projectNames collects one character's Chinese names in write order: the main
// `简体中文名` first (so it is the one that can claim the locale primary), then
// the Chinese-declaring `别名` items in document order. Duplicates within the
// character are dropped — a repeated 第二中文名 must not cost a second write
// attempt. rejected counts non-empty values the Chinese test refused.
func projectNames(fields []field) (names []string, rejected int) {
	seen := map[string]bool{}
	take := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if !isChineseName(v) {
			rejected++
			return
		}
		if seen[v] {
			return
		}
		seen[v] = true
		names = append(names, v)
	}
	for _, f := range fields {
		if f.Key == keyPrimaryZh {
			take(f.Value)
		}
	}
	for _, f := range fields {
		if f.Key != keyAliasList {
			continue
		}
		for _, it := range f.Items {
			if chineseItemKeys[it.Key] {
				take(it.Value)
			}
		}
	}
	return names, rejected
}

// isChineseName is the Chinese test: at least one Han character and no kana.
//
// The kana veto uses the Unicode SCRIPT tables, not the code blocks: `・`
// (U+30FB) and `ー` (U+30FC) live in the Katakana block but are script Common,
// and both are ordinary punctuation in Chinese renderings of foreign names
// (鲁路修・兰佩路基). Vetoing on the block would drop them.
//
// Requiring a Han character is what removes the main field's junk tail — Latin
// transcriptions ("Cock Robin", "PG-7") and `？？？` sentinels, ~110 rows of the
// anchored supply. It also, deliberately, drops a hypothetical all-kana value:
// that is a Japanese name filed under the wrong key, not a Chinese one.
func isChineseName(s string) bool {
	han := false
	for _, r := range s {
		if unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) {
			return false
		}
		if unicode.Is(unicode.Han, r) {
			han = true
		}
	}
	return han
}
