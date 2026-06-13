// Package intronorm normalizes legacy VNDB-imported English galgame intros.
//
// Wiki intros are Markdown, but the English ones were bulk-imported long ago
// from VNDB descriptions that use BBCode, so ~half of intro_en_us still carry
// raw BBCode that a Markdown renderer shows as literal junk:
//
//   - source-attribution lines anywhere in the text — "[From [url=…]getchu
//     [/url]]", "[From ErogeShop]", "[Edited from …]", "(edited from …)",
//     "*[From …]" (often backslash-escaped, sometimes mid-document in a
//     multi-description blurb) → DELETED.
//   - inline links "[url=X]Y[/url]" / "[url]X[/url]" → Markdown link [Y](X) / <X>.
//   - every other BBCode tag ([b] [i] [spoiler] [quote] [code] [color] …) →
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
	// [url=X]Y[/url] → [Y](X). Tolerates escapes, <url> wrapping, and a malformed
	// closing tag like [/url>] (a stray ">" from a broken <url>).
	reURLLabeled = regexp.MustCompile(`(?is)\\?\[url=\s*<?\s*([^\]<>\s]+?)\s*>?\s*\\?\]([\s\S]*?)\\?\[/url\s*>?\s*\\?\]`)
	// [url]X[/url] → <X>.
	reURLBare = regexp.MustCompile(`(?is)\\?\[url\\?\]\s*<?([^\[<>\s]+?)>?\s*\\?\[/url\s*>?\s*\\?\]`)
	// Any remaining (whitelisted) BBCode tag — opening or closing, with optional
	// =value, optional escape and a stray ">" before "]". Stripped; inner text is
	// kept. `url` mops up ORPHAN url tags reURLLabeled/reURLBare couldn't pair up.
	reBBTag = regexp.MustCompile(`(?i)\\?\[/?(?:url|b|i|u|s|strike|spoiler|quote|code|raw|center|left|right|justify|color|size|sub|sup|list|\*|h[1-6]|font|heading)(?:=[^\]]*)?\s*>?\s*\\?\]`)
	// Inline-appended trailing attribution (not on its own line — tacked onto the
	// end of the last sentence): "…story. [From [x](url)]". Anchored on "from"
	// (optionally after up to 3 words) + a link gate in stripTrailer, so a
	// legitimate trailing link like "[Source code](url)" is left alone.
	reTrailerCandidate = regexp.MustCompile(`(?i)\s*(?:\\?[*>][ \t]*)?\\?[\[(]\s*(?:[a-z]+ ){0,3}from\b[^\n]*$`)
	reTrailingWS       = regexp.MustCompile(`[ \t]+\n`)
	reMultiBlank       = regexp.MustCompile(`\n{3,}`)
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

	// 2. Drop standalone source-attribution lines ANYWHERE (not just trailing):
	//    own-line "[From …]" / "(edited from …)" / "*[From …]". A mid-document
	//    line must carry a link to qualify (these multi-blurb attributions always
	//    do) so a stray bracketed aside isn't removed; the final line may be a
	//    link-less "[From ErogeShop]". Done before conversion so the line's messy
	//    inner [url]/[/url>] is removed wholesale.
	s = dropAttributionLines(s)

	// 3. Links → Markdown (labeled first, then bare).
	s = reURLLabeled.ReplaceAllString(s, "[$2]($1)")
	s = reURLBare.ReplaceAllString(s, "<$1>")

	// 4. Strip remaining BBCode tags, keep inner text.
	s = reBBTag.ReplaceAllString(s, "")

	// 5. Remove a trailing sourced attribution appended inline to a content line
	//    (not its own line, so step 2 left it). Link-gated; idempotent.
	s = stripTrailer(s)

	// Decide substantive change against the line-ending-folded base (so a pure
	// CRLF intro is NOT considered changed and won't be rewritten).
	if s == base {
		return in, nil, false
	}

	// 6. Tidy whitespace left behind by removals — only on rows we changed.
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
// Kept broad (incl. the common "fom" typo); detection also requires the line to
// be wholly bracketed AND (mid-document) to carry a link, so a stray verb in
// prose can't trigger it.
var attributionVerbs = []string{
	"from", "edited", "translated", "translation", "taken", "adapted", "based",
	"source", "modified", "condensed", "summarized", "description", "roughly",
	"slightly", "vague", "paraphrased", "reworded", "partially", "retrieved",
	"copied", "courtesy", "official", "rewritten", "compiled", "fom",
}

// dropAttributionLines removes whole lines that are standalone VNDB source
// attributions, anywhere in the text. A whole line wrapped in [..]/(..) and led
// by an attribution verb is metadata, not prose (real paragraphs don't bracket
// an entire line), so it goes whether or not it carries a link — covering both
// linked blurb attributions ("[Edited from [wiki](url)]") and link-less shop
// credits ("[From ErogeShop]", "[From Himeya Shop]").
func dropAttributionLines(s string) string {
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, ln := range lines {
		if isAttributionLine(ln) {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

// isAttributionLine reports whether a whole line is a standalone attribution:
// wrapped in [..] or (..) (optionally escaped / markdown-prefixed by * or >)
// and led by an attribution verb. The whole-line bracket + verb is the signal;
// a link is not required. A verb-less bracketed aside ("[A note here]") and an
// inline mention inside a sentence are both left untouched (the latter isn't a
// whole bracketed line).
func isAttributionLine(line string) bool {
	t := strings.TrimSpace(line)
	t = strings.TrimLeft(t, "\\*> \t")
	if !strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "(") {
		return false
	}
	// Tolerate a trailing period and a "}" typo'd in place of "]" (both seen in
	// the wild: "[From Wikipedia].", "[From F95Zone}").
	if end := strings.TrimRight(t, " \t."); !strings.HasSuffix(end, "]") &&
		!strings.HasSuffix(end, ")") && !strings.HasSuffix(end, `\]`) &&
		!strings.HasSuffix(end, "}") {
		return false
	}
	return hasAttributionVerb(strings.ToLower(t[1:]))
}

func hasAttributionVerb(low string) bool {
	for _, v := range attributionVerbs {
		if low == v || strings.HasPrefix(low, v+" ") || strings.HasPrefix(low, v+"]") {
			return true
		}
	}
	return false
}

// stripTrailer removes a trailing sourced attribution appended INLINE to a
// content line (so dropAttributionLines, which only drops whole attribution
// lines, left it). Anchored on "from" + gated on a link signal so prose like
// "(Based on a true story)" is never touched. Looped for the stacked case.
func stripTrailer(s string) string {
	s = strings.TrimRight(s, " \t\n")
	for range 3 {
		loc := reTrailerCandidate.FindStringIndex(s)
		if loc == nil {
			break
		}
		seg := strings.ToLower(s[loc[0]:])
		if !strings.Contains(seg, "http") && !strings.Contains(seg, "](") &&
			!strings.Contains(seg, "[url") && !strings.Contains(seg, "[/url") {
			break
		}
		s = strings.TrimRight(s[:loc[0]], " \t\n")
	}
	return s
}
