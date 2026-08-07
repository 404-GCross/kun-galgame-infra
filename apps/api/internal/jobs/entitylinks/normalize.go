package entitylinks

import (
	"regexp"
	"strings"
)

// The typed-tier normalizers and the Bangumi infobox key classifier are a
// deliberate TRANSCRIPTION of internal/jobs/orglabels/enrich_link.go rather than
// a shared helper. They are the wire format of an external_id already in the
// table: the value they produce must stay byte-identical to what E2 wrote, so
// the copy is pinned here and read as a contract, not refactored for reuse
// across two packages that must be free to change at different times. Any edit
// here without the same edit there re-splits rows that used to dedup.

var (
	reArchiveWrap = regexp.MustCompile(`(?i)web\.archive\.org/web/[^/]+/(https?://.+)$`)
	reScheme      = regexp.MustCompile(`(?i)^(https?:)?//`)
	reTwitterHost = regexp.MustCompile(`(?i)^(https?://)?(www\.|mobile\.)?(twitter\.com|x\.com)/`)
	reHandle      = regexp.MustCompile(`^[A-Za-z0-9_]{1,30}$`)
	reDigits      = regexp.MustCompile(`^[0-9]+$`)
)

// normalizeURL strips the archive wrapper, the scheme and any trailing slash so
// only protocol / trailing-slash differences dedup. Requires a dotted host.
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

// normalizeTwitter reduces a handle or profile URL to the bare lowercase handle.
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

// normalizeNumeric keeps a bare numeric id (cien creator, pixiv user).
func normalizeNumeric(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !reDigits.MatchString(v) {
		return "", false
	}
	return v, true
}

// ── Bangumi infobox key classification ──────────────────────────────────────

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

// classifyBGMKey buckets an infobox key. Website keys exclude store/social
// aliases (weibo/blog/steam/dlsite/…) that also carry a URL. The twitter class
// is kept even though this job's Bangumi sub-lane only consumes the website
// one — dropping it would quietly reclassify a "X (Twitter)" key as a website.
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
