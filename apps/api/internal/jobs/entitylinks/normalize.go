package entitylinks

import (
	"regexp"
	"strings"
)

var (
	reArchiveWrap = regexp.MustCompile(`(?i)web\.archive\.org/web/[^/]+/(https?://.+)$`)
	reScheme      = regexp.MustCompile(`(?i)^(https?:)?//`)
	reTwitterHost = regexp.MustCompile(`(?i)^(https?://)?(www\.|mobile\.)?(twitter\.com|x\.com)/`)
	reHandle      = regexp.MustCompile(`^[A-Za-z0-9_]{1,30}$`)
	reDigits      = regexp.MustCompile(`^[0-9]+$`)
)

func normalizeURL(u string) (string, bool) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", false
	}
	if m := reArchiveWrap.FindStringSubmatch(u); m != nil {
		u = m[1]
	}
	u = reScheme.ReplaceAllString(u, "")
	u = strings.TrimRight(u, "/")
	u = strings.TrimSpace(u)
	if u == "" || !strings.Contains(u, ".") || strings.ContainsAny(u, " \t") {
		return "", false
	}
	return u, true
}

func normalizeTwitter(v string) (string, bool) {
	v = strings.TrimSpace(v)
	v = reTwitterHost.ReplaceAllString(v, "")
	v = strings.TrimPrefix(v, "@")
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if !reHandle.MatchString(v) {
		return "", false
	}
	return v, true
}

func normalizeNumeric(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !reDigits.MatchString(v) {
		return "", false
	}
	return v, true
}

type bgmKeyClass int

const (
	bgmKeyNone bgmKeyClass = iota
	bgmKeyWebsite
	bgmKeyTwitter
)

var (
	reWebsiteKey = regexp.MustCompile(`官网|官方网站|官方網站|官方网址|官方網址|网站|網站|主页|主頁|website|homepage`)
	reWebExclude = regexp.MustCompile(`(?i)微博|博客|blog|weibo|steam|dlsite|facebook|youtube|niconico|bilibili|pixiv|instagram`)
	reTwitterKey = regexp.MustCompile(`(?i)twitter|^x\b|x\s*\(twitter\)|x\(twitter\)`)
)

func classifyBGMKey(key string) bgmKeyClass {
	k := strings.TrimSpace(key)
	if reWebExclude.MatchString(k) {
		if reTwitterKey.MatchString(k) {
			return bgmKeyTwitter
		}
		return bgmKeyNone
	}
	if reTwitterKey.MatchString(k) {
		return bgmKeyTwitter
	}
	if reWebsiteKey.MatchString(strings.ToLower(k)) || reWebsiteKey.MatchString(k) {
		return bgmKeyWebsite
	}
	return bgmKeyNone
}
