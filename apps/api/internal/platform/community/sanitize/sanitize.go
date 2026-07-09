// Package sanitize is the community primitive's write-time content pipeline
// (doc 11 invariant 6): Markdown raw → rendered HTML → whitelist-sanitized
// "cooked" HTML, stored alongside the raw source with the SanitizerVersion that
// produced it. This is the platform-level home of the "shared write-time
// whitelist" (todos) — letmoe's per-repo sanitizer generalized.
//
// Two layers of defense, sanitizer-authoritative:
//  1. goldmark renders Markdown with raw inline HTML ESCAPED (no WithUnsafe),
//     so a user who types `<script>` gets inert text, never a tag.
//  2. bluemonday's UGC policy is then applied to goldmark's output — the
//     authoritative whitelist (strips script/style/on*/dangerous schemes, adds
//     rel=nofollow, restricts URL schemes to http/https/mailto).
package sanitize

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// SanitizerVersion pins the whitelist + render pipeline. Bump by 1 on any
// MATERIAL change (a newly allowed/denied tag or attribute, a render option
// that changes output); posts carry the version that cooked them so a future
// job can re-cook only the stale ones. The re-cook job itself is not part of
// this step.
const SanitizerVersion = 1

var (
	// GFM: tables / strikethrough / autolinks / task lists. Raw HTML stays
	// escaped (goldmark's default without WithUnsafe) — the safest UGC posture.
	md = goldmark.New(goldmark.WithExtensions(extension.GFM))

	// UGCPolicy is bluemonday's canonical user-generated-content whitelist:
	// standard formatting + links (rel=nofollow, http/https/mailto only) +
	// images, with script/style/event-handlers/dangerous schemes stripped.
	policy = bluemonday.UGCPolicy()

	// mentionRe counts @username tokens in the RAW source (before render): an @
	// preceded by a non-word char (so foo@bar.com email local parts do NOT
	// count) followed by a username. Used only as a TL0 sandbox input.
	mentionRe = regexp.MustCompile(`(?:^|[^\w@])@[A-Za-z0-9_\-]{1,32}`)
)

// Cooked is the write-time product: the sanitized HTML, the version that made
// it, and the counts the TL0 sandbox needs (computed once here so the service
// never re-parses the body).
type Cooked struct {
	HTML     string
	Version  int
	Links    int // sanitized <a> anchors (includes GFM autolinked bare URLs)
	Images   int // sanitized <img> elements
	Mentions int // @username tokens in the raw source
}

// Cook renders + sanitizes raw Markdown into a Cooked result. It never returns
// an error: goldmark.Convert does not fail on an in-memory source, and the
// bluemonday pass is the safety authority regardless.
func Cook(raw string) Cooked {
	var buf bytes.Buffer
	if err := md.Convert([]byte(raw), &buf); err != nil {
		// Defensive: emit sanitized raw rather than anything unsanitized.
		return Cooked{HTML: policy.Sanitize(raw), Version: SanitizerVersion, Mentions: countMentions(raw)}
	}
	html := string(policy.SanitizeBytes(buf.Bytes()))
	return Cooked{
		HTML:     html,
		Version:  SanitizerVersion,
		Links:    strings.Count(html, "<a "),
		Images:   strings.Count(html, "<img "),
		Mentions: countMentions(raw),
	}
}

func countMentions(raw string) int {
	return len(mentionRe.FindAllStringIndex(raw, -1))
}
