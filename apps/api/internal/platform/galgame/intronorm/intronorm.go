// Package intronorm normalizes legacy VNDB-imported English galgame intros.
//
// Wiki intros are Markdown, but the English ones were bulk-imported long ago
// from VNDB descriptions that use BBCode, so ~half of intro_en_us still carry
// raw BBCode that a Markdown renderer shows as literal junk:
//
//   - a trailing attribution line: "[From [url=...]getchu[/url]]", "[From
//     ErogeShop]", "[Edited from ...]" (sometimes backslash-escaped) → DELETED.
//   - inline links "[url=X]Y[/url]" / "[url]X[/url]" → Markdown link [Y](X) / <X>.
//   - every other BBCode tag ([b] [i] [spoiler] [quote] [code] [color] ...) →
//     stripped, inner text kept as plain text.
//   - images (Markdown ![](url) and BBCode [img]url[/img]) → removed from the
//     text entirely (collected for an export manifest; NOT migrated).
//
// Only English is normalized — the other languages are user-authored Markdown
// and never went through the VNDB BBCode path.
//
// The transform is pure and idempotent: a clean intro returns unchanged
// (changed=false, original string untouched, no whitespace churn). It is the
// single source of truth shared by the one-off cleanup cmd and (later) the
// write-path guard, so it carries its own table-driven tests.
package intronorm

import (
	"regexp"
	"strings"
)

var (
	// Markdown image: ![alt](url ...optional title). Captures the URL.
	reImgMarkdown = regexp.MustCompile(`!\[[^\]]*\]\(\s*<?([^)>\s]+)[^)]*\)`)
	// BBCode image: [img]url[/img] or [img=url]...[/img] (optionally escaped).
	reImgBBCode = regexp.MustCompile(`(?is)\\?\[img(?:=([^\]]*))?\\?\]\s*<?([^\[<>\s]*)[^\[]*?\\?\[/img\\?\]`)
	// [url=X]Y[/url] → [Y](X). Tolerates escapes and <url> wrapping.
	reURLLabeled = regexp.MustCompile(`(?is)\\?\[url=\s*<?\s*([^\]<>\s]+?)\s*>?\s*\\?\]([\s\S]*?)\\?\[/url\\?\]`)
	// [url]X[/url] → <X>.
	reURLBare = regexp.MustCompile(`(?is)\\?\[url\\?\]\s*<?([^\[<>\s]+?)>?\s*\\?\[/url\\?\]`)
	// Any remaining (whitelisted) BBCode tag — opening or closing, with optional
	// =value and optional escape. Stripped; inner text is kept.
	reBBTag = regexp.MustCompile(`(?i)\\?\[/?(?:b|i|u|s|strike|spoiler|quote|code|raw|center|left|right|justify|color|size|sub|sup|list|\*|h[1-6]|font|heading)(?:=[^\]]*)?\\?\]`)
	reTrailingWS = regexp.MustCompile(`[ \t]+\n`)
	reMultiBlank = regexp.MustCompile(`\n{3,}`)
)

// normalizeLineEndings folds CRLF / CR to LF.
func normalizeLineEndings(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// NormalizeEnglishIntro returns the cleaned intro, the image URLs it removed,
// and whether a substantive change was made. When changed is false the input
// is returned verbatim (no whitespace-only churn, so callers can skip the row).
func NormalizeEnglishIntro(in string) (out string, removedImages []string, changed bool) {
	base := normalizeLineEndings(in)
	s := base

	// 1. Images (BBCode [img] first so its URL isn't mangled by later steps,
	//    then Markdown ![](...)). Collect URLs, drop the markup.
	for _, m := range reImgBBCode.FindAllStringSubmatch(s, -1) {
		if u := firstNonEmpty(m[1], m[2]); u != "" {
			removedImages = append(removedImages, u)
		}
	}
	s = reImgBBCode.ReplaceAllString(s, "")
	for _, m := range reImgMarkdown.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			removedImages = append(removedImages, m[1])
		}
	}
	s = reImgMarkdown.ReplaceAllString(s, "")

	// 2. Remove trailing VNDB attribution line(s): a standalone, wholly
	//    bracketed final line — [From ...], [Translated from ...], [Edited
	//    from ...], [Source: ...], even typos. Structural detection (whole
	//    line bracketed + a link or attribution verb) covers every verb
	//    variant without an exhaustive whitelist. Up to 2 stacked lines.
	s = strings.TrimRight(s, " \t\n")
	for range 2 {
		nl := strings.LastIndexByte(s, '\n')
		if !isAttributionLine(s[nl+1:]) {
			break
		}
		s = strings.TrimRight(s[:nl+1], " \t\n")
	}

	// 3. Links → Markdown (labeled first, then bare).
	s = reURLLabeled.ReplaceAllString(s, "[$2]($1)")
	s = reURLBare.ReplaceAllString(s, "<$1>")

	// 4. Strip remaining BBCode tags, keep inner text.
	s = reBBTag.ReplaceAllString(s, "")

	// Decide substantive change against the line-ending-folded base (so a pure
	// CRLF intro is NOT considered changed and won't be rewritten).
	if s == base {
		return in, nil, false
	}

	// 5. Tidy whitespace left behind by removals — only on rows we changed.
	s = reTrailingWS.ReplaceAllString(s, "\n")
	s = reMultiBlank.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)

	return s, removedImages, true
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// attributionVerbs are the leading words of a VNDB source-attribution line.
// Kept broad (incl. the common "fom" typo) because detection ALSO requires the
// line to be wholly bracketed, so a stray verb in prose can't trigger it.
var attributionVerbs = []string{
	"from", "edited", "translated", "translation", "taken", "adapted", "based",
	"source", "modified", "condensed", "summarized", "description", "roughly",
	"slightly", "vague", "paraphrased", "reworded", "partially", "retrieved",
	"copied", "courtesy", "official", "rewritten", "compiled", "fom",
}

// isAttributionLine reports whether one line is a standalone VNDB attribution:
// wholly wrapped in (optionally escaped) brackets AND carrying a link or a known
// attribution verb. Requiring both bracket-wrapping and a signal avoids nuking a
// legitimate short bracketed last line like "[END]" or a markdown link/footnote
// (which ends in ")" or ":" , not "]").
func isAttributionLine(line string) bool {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, `\`)
	if !strings.HasPrefix(t, "[") {
		return false
	}
	if end := strings.TrimRight(t, " \t"); !strings.HasSuffix(end, "]") && !strings.HasSuffix(end, `\]`) {
		return false
	}
	low := strings.ToLower(t[1:])
	if strings.Contains(low, "[url") || strings.Contains(low, "http") {
		return true
	}
	for _, v := range attributionVerbs {
		if low == v || strings.HasPrefix(low, v+" ") || strings.HasPrefix(low, v+"]") {
			return true
		}
	}
	return false
}
