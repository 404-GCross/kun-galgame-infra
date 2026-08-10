package intromt

import "regexp"

var (
	reVNDBLink = regexp.MustCompile(`\[([^\]]*)\]\(/[a-z]+[0-9]+[^)]*\)`)
	reBBURL    = regexp.MustCompile(`\[url=[^\]]*\]([\s\S]*?)\[/url\]`)
	reBBSpoil  = regexp.MustCompile(`\[/?(?:spoiler|quote|b|i|u|s)\]`)
	reBBRaw    = regexp.MustCompile(`\[/?raw\]`)
)

func sanitizeSource(s string) string {
	s = reVNDBLink.ReplaceAllString(s, "$1")
	s = reBBURL.ReplaceAllString(s, "$1")
	s = reBBSpoil.ReplaceAllString(s, "")
	s = reBBRaw.ReplaceAllString(s, "")
	return s
}
