package bgmzhnames

import (
	"encoding/json"
	"strings"
	"unicode"
)

const (
	keyPrimaryZh = "简体中文名"
	keyAliasList = "别名"
)

var chineseItemKeys = map[string]bool{
	"中文名": true, "第二中文名": true, "第三中文名": true, "第四中文名": true,
	"简体中文名": true, "簡體中文名": true, "繁体中文名": true, "繁體中文名": true,
	"中文译名": true, "中文譯名": true,
}

type field struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
	Items []item `json:"Items"`
}

type item struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

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
	return fields, fields != nil
}

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
