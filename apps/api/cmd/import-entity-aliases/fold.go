package main

import (
	"strings"
	"unicode"
)

// Name-folding — identical semantics to cmd/person-link-batch (step 24). The
// input is already NFKC-normalized by SQL `normalize(name, NFKC)` (so fullwidth
// parens/comma are ASCII; the Silver expression, no Go NFKC dep). Kept local by
// deliberate duplication (~40 lines of pure string logic) so this step does not
// refactor the already-deployed step-24 tool; the fold-parity test pins the
// shared examples.

// foldName is the comparison key: drop every (...) segment, remove whitespace,
// lowercase.
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

// parenItemsRaw returns the RAW (trimmed, un-folded) alias substrings inside
// every top-level (...) segment, split on the ideographic comma 、 (the only
// in-paren separator in the data; ASCII , honored defensively). Raw because
// storage needs the spaces/case that foldName strips.
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

// parenAliases returns the FOLDED alias items (for cross-source comparison).
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

// roleDisambiguators are parenthetical role tags that are NOT aliases —
// "七瀬(声優)" means "the voice actor named 七瀬", not an alternate name. Copied
// from internal/platform/catalog/llmsuggest (step 12's gold-set filter); a tiny
// vocabulary kept local rather than exported across a package boundary.
var roleDisambiguators = map[string]bool{
	"声優": true, "声优": true, "歌手": true, "原画": true, "シナリオ": true,
	"音楽": true, "監督": true, "脚本": true, "作曲": true, "編曲": true,
	"作詞": true, "主題歌": true, "ボーカル": true, "vocal": true, "cv": true,
	"イラスト": true, "絵": true, "画": true, "文": true, "曲": true, "歌": true,
}

// isRoleTag reports whether a raw alias value is purely a role annotation
// (compared trimmed + lowercased, matching the gold-set filter).
func isRoleTag(raw string) bool {
	return roleDisambiguators[strings.ToLower(strings.TrimSpace(raw))]
}
