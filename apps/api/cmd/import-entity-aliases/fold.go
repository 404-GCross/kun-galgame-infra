package main

import (
	"strings"
	"unicode"
)

func foldName(nfkc string) string {
	return strings.ToLower(removeSpaces(stripParens(nfkc)))
}

func stripParens(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func removeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func parenItemsRaw(nfkc string) []string {
	var items []string
	depth := 0
	var cur strings.Builder
	flush := func() {
		for _, part := range strings.FieldsFunc(cur.String(), func(r rune) bool { return r == '、' || r == ',' }) {
			if p := strings.TrimSpace(part); p != "" {
				items = append(items, p)
			}
		}
		cur.Reset()
	}
	for _, r := range nfkc {
		switch r {
		case '(':
			if depth == 0 {
				cur.Reset()
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
				if depth == 0 {
					flush()
				}
			}
		default:
			if depth > 0 {
				cur.WriteRune(r)
			}
		}
	}
	return items
}

func parenAliases(nfkc string) []string {
	raw := parenItemsRaw(nfkc)
	out := make([]string, 0, len(raw))
	for _, it := range raw {
		if f := foldName(it); f != "" {
			out = append(out, f)
		}
	}
	return out
}

var roleDisambiguators = map[string]bool{
	"声優": true, "声优": true, "歌手": true, "原画": true, "シナリオ": true,
	"音楽": true, "監督": true, "脚本": true, "作曲": true, "編曲": true,
	"作詞": true, "主題歌": true, "ボーカル": true, "vocal": true, "cv": true,
	"イラスト": true, "絵": true, "画": true, "文": true, "曲": true, "歌": true,
}

func isRoleTag(raw string) bool {
	return roleDisambiguators[strings.ToLower(strings.TrimSpace(raw))]
}
