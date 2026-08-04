package intromt

import "regexp"

// VNDB writes its blurbs in its own markup, and the model carries it through
// verbatim: all 8 works in the first en→zh run whose English carried markup
// produced Chinese carrying the same markup. Both forms are actively wrong once
// the text is on our pages —
//
//	[远野志贵](/c72)   a link relative to OUR domain, so it 404s
//	[url=https://…]t[/url]   BBCode we do not render, shown as literal text
//
// — so the source is cleaned before it is translated, not after. Cleaning
// first also makes the fix self-healing: the source hash is taken from the
// cleaned text, so rows translated from the dirty version no longer match and
// the lane re-translates exactly those, with no backfill script.
//
// Only the MARKUP is removed; the link text is kept, because it is part of the
// sentence ("Toono Shiki hears of…" reads as a sentence, "hears of…" does not).
var (
	// [text](/c72) and [text](/v7) — VNDB's internal entity links. Deliberately
	// anchored on a leading slash so genuine absolute URLs are left alone.
	reVNDBLink = regexp.MustCompile(`\[([^\]]*)\]\(/[a-z]+[0-9]+[^)]*\)`)
	// [url=…]text[/url], and the bare [url] / [/url] pair.
	reBBURL   = regexp.MustCompile(`\[url=[^\]]*\]([\s\S]*?)\[/url\]`)
	reBBSpoil = regexp.MustCompile(`\[/?(?:spoiler|quote|b|i|u|s)\]`)
	// [raw]…[/raw] wrappers VNDB uses around untranslated titles.
	reBBRaw = regexp.MustCompile(`\[/?raw\]`)
)

// sanitizeSource strips upstream markup from a blurb before it is hashed and
// translated.
func sanitizeSource(s string) string {
	s = reVNDBLink.ReplaceAllString(s, "$1")
	s = reBBURL.ReplaceAllString(s, "$1")
	s = reBBSpoil.ReplaceAllString(s, "")
	s = reBBRaw.ReplaceAllString(s, "")
	return s
}
