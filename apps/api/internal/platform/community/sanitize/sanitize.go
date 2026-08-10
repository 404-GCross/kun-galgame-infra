package sanitize

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

const SanitizerVersion = 1

var (
	md = goldmark.New(goldmark.WithExtensions(extension.GFM))

	policy = bluemonday.UGCPolicy()

	mentionRe = regexp.MustCompile(`(?:^|[^\w@])@[A-Za-z0-9_\-]{1,32}`)
)

type Cooked struct {
	HTML     string
	Version  int
	Links    int
	Images   int
	Mentions int
}

func Cook(raw string) Cooked {
	var buf bytes.Buffer
	if err := md.Convert([]byte(raw), &buf); err != nil {
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
