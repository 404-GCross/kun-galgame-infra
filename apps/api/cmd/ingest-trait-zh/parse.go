package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type pair struct {
	En   string
	Zh   string
	Line int
}

var (
	kvLine          = regexp.MustCompile(`^\s*"((?:[^"\\]|\\.)*)"\s*:\s*"((?:[^"\\]|\\.)*)"\s*,?\s*(?:/[/*].*)?$`)
	ruleName        = regexp.MustCompile(`^\s*name\s*:\s*'.*` + regexp.QuoteMeta("(标签与特征)"))
	mapOpen         = regexp.MustCompile(`^\s*map\s*:\s*\{\s*$`)
	mapClose        = regexp.MustCompile(`^\s*titleMap\s*:`)
	sectionMarker   = regexp.MustCompile(`/\*\s*todo\s*-{2,}(.*?)\*/`)
	traitSubheading = regexp.MustCompile(`^\s*//\s*` + regexp.QuoteMeta("特征") + `\s*$`)
)

const (
	markerCharTraits = "人物特征"
	markerCharsPage  = "chars#chars"
	markerVNTags     = "VN标签"
)

type parseOpts struct {
	IncludeTagVocab bool
}

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
		inRule   bool
		inMap    bool
		included = true
		seen     = map[string]bool{}
	)
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
			break
		}

		if m := sectionMarker.FindStringSubmatch(line); m != nil {
			included = isTraitSection(m[1], opts)
			continue
		}
		if traitSubheading.MatchString(line) {
			included = true
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
			continue
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

func stripQualityMarkers(s string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "°'"))
}

func unescapeJS(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(s)
}
