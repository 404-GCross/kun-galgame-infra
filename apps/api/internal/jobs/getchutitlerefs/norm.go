// Package getchutitlerefs anchors Getchu products that VNDB never linked, by
// agreement between the two catalogues rather than by a declared link
// (refs/proj/167 §13).
//
// WHY THIS EXISTS, AND WHY IT IS NOT getchurefs. getchurefs rides VNDB's own
// extlinks: VNDB records the getchu id on a release we have already anchored
// EXACT to VNDB, so the getchu ref is exactly as strong as the vndb ref and is
// written at the same tier. That covers 13,800 products. It cannot cover a
// product VNDB simply never linked — 7,806 crawled items had no anchor at all,
// and a third of them are games the catalog already holds.
//
// THE TIER RULE, which is the whole design constraint. `exact` in this catalog
// means a DECLARED identity, not a confident guess; that is what lets every
// consumer trust link_kind=exact without re-deriving it. Title matching is
// inference, so it produces `probable`, and this repo's standing rule is that
// probable anchors are not ingested from until an adjudication wave has looked
// at them (the 14,014 DMM rows have sat that way for exactly this reason).
//
// So this lane does the adjudication itself, with evidence, instead of
// skipping it. Two independent signal families must agree:
//
//	MATCH   normalized title + brand + exact release date, resolving to
//	        exactly one work and exactly one release
//	CONFIRM the character rosters overlap — a signal the match never looked at
//
// A row confirmed by both is written `exact` and stamped
// "rule:title-brand-date+roster"; a row that matched but could not be confirmed
// is written `probable` and stamped "rule:title-brand-date", so the tier and
// the evidence behind it are readable straight off the row.
//
// Measured on production before implementation: of 1,291 matches testable by
// roster, 1,267 (98.1%) overlapped, and every one of the non-overlapping cases
// inspected turned out to be a CORRECT match that the comparison could not see
// (romaji suffixes, a different name separator, a katakana/romaji split). There
// were no confirmed mis-matches.
package getchutitlerefs

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var (
	// Punctuation that separates or decorates a title but carries no identity.
	reTitleNoise = regexp.MustCompile(`[[:space:]　・！!？?~〜「」『』（）()【】\[\]/\\-]`)
	// Getchu appends a romaji gloss to some character names:
	// "澤樹 告 ＜Tsuguru Sawaki＞". Only 129 rows of 86,351 carry it, which is
	// why the crawler is left alone and it is stripped here instead.
	reAngleGloss = regexp.MustCompile(`[＜<][^＞>]*[＞>]`)
	// The two catalogues disagree about the separator inside a transliterated
	// personal name: Getchu writes シャルロッテ・カミル・ハーリンガム where the
	// catalog writes シャルロッテ＝カミル＝ハーリンガム. Neither spelling is
	// wrong and the distinction never identifies anyone.
	reNameSep = regexp.MustCompile(`[・＝=･·]`)
)

// NormTitle reduces a product title to its identity: NFKC, punctuation and
// spacing removed, lowercased.
func NormTitle(s string) string {
	return strings.ToLower(norm.NFKC.String(reTitleNoise.ReplaceAllString(s, "")))
}

// NormBrand reduces a brand/label name the same way, but keeps the punctuation
// a title may legitimately carry — a brand is a short token and stripping more
// invites collisions.
func NormBrand(s string) string {
	return strings.ToLower(norm.NFKC.String(
		regexp.MustCompile(`[[:space:]　・]`).ReplaceAllString(s, "")))
}

// NormPersonName reduces a character name for the ROSTER CONFIRMATION only. It
// is deliberately more forgiving than the title normalizer: the two catalogues
// transcribe the same person differently, and this comparison exists to notice
// agreement, not to establish identity — the match has already been made by
// then, and a false negative merely leaves a row at `probable`.
func NormPersonName(s string) string {
	s = reAngleGloss.ReplaceAllString(s, "")
	s = reNameSep.ReplaceAllString(s, "")
	s = regexp.MustCompile(`[[:space:]　]`).ReplaceAllString(s, "")
	return strings.ToLower(norm.NFKC.String(s))
}
