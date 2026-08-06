package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// pair is one en→zh trait-name rendering lifted from the userscript, with the
// source line kept for the dry report (so a surprising rendering is traceable
// back to the exact line the human wrote it on).
type pair struct {
	En   string
	Zh   string
	Line int
}

// The userscript is a Tampermonkey JS file, not JSON: the maps are object
// literals whose entries are `"key": "value",` lines, freely interleaved with
// //- and /*-comments, IDEA fold markers and even a `//"Key": "value",` line
// the author commented out. A line-oriented scan over the identified sections
// is therefore both simpler and more robust than a JS parser, and it is what
// lets the section boundaries (themselves comments) be honoured at all.
var (
	// kvLine matches an active `"en": "zh",` entry. Anchored at the line start
	// (after indentation) so a commented-out `//"Key": "value",` never matches.
	kvLine = regexp.MustCompile(`^\s*"((?:[^"\\]|\\.)*)"\s*:\s*"((?:[^"\\]|\\.)*)"\s*,?\s*(?:/[/*].*)?$`)
	// ruleName finds the rule whose map holds the tag+trait vocabulary.
	ruleName = regexp.MustCompile(`^\s*name\s*:\s*'.*` + regexp.QuoteMeta("(标签与特征)"))
	mapOpen  = regexp.MustCompile(`^\s*map\s*:\s*\{\s*$`)
	mapClose = regexp.MustCompile(`^\s*titleMap\s*:`)
	// sectionMarker is the `/*todo ----<label>*/` comment that separates the
	// contributed blocks inside that map.
	sectionMarker = regexp.MustCompile(`/\*\s*todo\s*-{2,}(.*?)\*/`)
	// traitSubheading is the `//特征` sub-comment that starts the character-trait
	// tail of the block whose own marker says it holds VN tags. The block is
	// mixed: its first ~2,300 lines are VN tags, its last ~245 are traits
	// (毛发/眼睛/身体). Without this anchor every hair/eye/body colour is lost.
	traitSubheading = regexp.MustCompile(`^\s*//\s*` + regexp.QuoteMeta("特征") + `\s*$`)
)

// section labels. A marker naming 人物特征 (character traits) or citing a VNDB
// character page (…/chars#chars) opens a trait section; anything else closes
// one. The unlabelled leading section (before the first marker) is the
// hand-curated block — VNDB group headings plus the author's own trait
// renderings — and is always included.
const (
	markerCharTraits = "人物特征"
	markerCharsPage  = "chars#chars"
	markerVNTags     = "VN标签"
)

// parseOpts selects which sections of the script are read.
type parseOpts struct {
	// IncludeTagVocab additionally reads the VN-TAG sections of the same map.
	// Off by default and deliberately so: those sections translate VNDB *tags*,
	// a different vocabulary that happens to share ~365 names with the trait
	// table (almost all of them in the 色情内容 block). Their renderings are
	// usually right, but "usually" is not the standard for a curated
	// provenance-0 write — flip this on only with a human reading the diff.
	IncludeTagVocab bool
}

// parseScript extracts the en→zh trait renderings from the userscript.
//
// Section boundaries are found by their comment markers, never by absolute line
// numbers: the upstream script is a living file and every one of these blocks
// has moved at least once across its releases.
func parseScript(path string, opts parseOpts) ([]pair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		out      []pair
		sc       = bufio.NewScanner(f)
		lineNo   int
		inRule   bool // inside the (标签与特征) rule
		inMap    bool // inside that rule's map{}
		included = true
		seen     = map[string]bool{}
	)
	// One entry carries a multi-paragraph explanatory comment; the default 64KB
	// token limit is ample, but a generous ceiling costs nothing.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		lineNo++
		line := sc.Text()

		if !inRule {
			if ruleName.MatchString(line) {
				inRule = true
			}
			continue
		}
		if !inMap {
			if mapOpen.MatchString(line) {
				inMap = true
			}
			continue
		}
		if mapClose.MatchString(line) {
			break // the rule's map ended — everything after is another rule
		}

		if m := sectionMarker.FindStringSubmatch(line); m != nil {
			included = isTraitSection(m[1], opts)
			continue
		}
		if traitSubheading.MatchString(line) {
			included = true // trait tail of a mixed block
			continue
		}
		if !included {
			continue
		}
		m := kvLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		en, zh := unescapeJS(m[1]), stripQualityMarkers(unescapeJS(m[2]))
		if en == "" || zh == "" || seen[en] {
			continue // first writing of a key wins — deterministic; the script's map carries no duplicate keys today (verified over the whole 615-6222 range)
		}
		seen[en] = true
		out = append(out, pair{En: en, Zh: zh, Line: lineNo})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !inMap {
		return nil, fmt.Errorf("%s: the (标签与特征) rule map was not found — did the script layout change?", path)
	}
	return out, nil
}

// isTraitSection decides whether the block a marker opens is trait vocabulary.
func isTraitSection(label string, opts parseOpts) bool {
	switch {
	case strings.Contains(label, markerCharTraits), strings.Contains(label, markerCharsPage):
		return true
	case opts.IncludeTagVocab && strings.Contains(label, markerVNTags):
		return true
	default:
		return false
	}
}

// stripQualityMarkers removes the author's own confidence suffixes, documented
// at the top of the script: ° = rough translation, ' = no accurate rendering
// found. They are editorial notes to the script's maintainers, not part of the
// name, and would otherwise be rendered to end users verbatim.
func stripQualityMarkers(s string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "°'"))
}

// unescapeJS resolves the escape sequences a JS string literal may carry. Only
// the ones this file actually uses appear (\" and \\); anything else is left
// alone rather than guessed at.
func unescapeJS(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s)
}
