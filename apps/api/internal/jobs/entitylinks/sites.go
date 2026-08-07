package entitylinks

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ── typed tier ──────────────────────────────────────────────────────────────

// typedSite maps a VNDB extlink site onto an existing first-class
// catalog_source. The normalizer is deliberately the SAME one the E2 org wave
// (internal/jobs/orglabels/enrich_link.go) and the curated link field
// (internal/platform/catalog/editspec/work_links.go) apply, so the external_id
// this job writes is byte-identical to theirs and the primary key dedups across
// waves instead of storing http/https + trailing-slash twins.
type typedSite struct {
	sourceKey string
	normalize func(string) (string, bool)
}

var typedSites = map[string]typedSite{
	"website": {"official_site", normalizeURL},
	"twitter": {"twitter", normalizeTwitter},
	"cien":    {"cien", normalizeNumeric},
	"pixiv":   {"pixiv", normalizeNumeric},
}

const officialSiteKey = "official_site"

// ── web-rendered tier ───────────────────────────────────────────────────────

// webTemplate renders a site-native value into the absolute URL stored as the
// `web` source's external_id.
//
// The table is transcribed from VNDB's own lib/VNDB/ExtLinks.pm — the module
// that owns the (site → URL) mapping for exactly these extlinks rows. Copying
// it is the only honest option: extlinks.value is the site-native id, never a
// URL, so without the template there is nothing to store, and inventing a URL
// shape would fabricate links that do not resolve.
type webTemplate struct {
	format string
	// allowSlash lets a value carry a path separator. Only substar needs it:
	// VNDB stores the host tail ("adult/name"), so the slash is part of the
	// id rather than a sign of a malformed value.
	allowSlash bool
	// numeric renders with %d instead of %s (imdb's zero-padded nm id).
	numeric bool
}

var webTemplates = map[string]webTemplate{
	// value is the bare Q-number.
	"wikidata":       {format: "https://www.wikidata.org/wiki/Q%s"},
	"wp":             {format: "https://en.wikipedia.org/wiki/%s"},
	"renai":          {format: "https://renai.us/game/%s"},
	"youtube":        {format: "https://www.youtube.com/@%s"},
	"bsky":           {format: "https://bsky.app/profile/%s"},
	"instagram":      {format: "https://www.instagram.com/%s/"},
	"facebook":       {format: "https://www.facebook.com/%s"},
	"tumblr":         {format: "https://%s.tumblr.com/"},
	"fanbox":         {format: "https://%s.fanbox.cc/"},
	"fantia":         {format: "https://fantia.jp/fanclubs/%s"},
	"patreon":        {format: "https://www.patreon.com/%s"},
	"scloud":         {format: "https://soundcloud.com/%s"},
	"weibo":          {format: "https://weibo.com/u/%s"},
	"bilibili":       {format: "https://space.bilibili.com/%s"},
	"nijie":          {format: "https://nijie.info/members.php?id=%s"},
	"booth_pub":      {format: "https://%s.booth.pm/"},
	"itch_dev":       {format: "https://%s.itch.io/"},
	"vk":             {format: "https://vk.com/%s"},
	"boosty":         {format: "https://boosty.to/%s"},
	"afdian":         {format: "https://afdian.com/a/%s"},
	"substar":        {format: "https://subscribestar.%s", allowSlash: true},
	"steam_curator":  {format: "https://store.steampowered.com/curator/%s"},
	"mobygames_comp": {format: "https://www.mobygames.com/company/%s"},
	"gamefaqs_comp":  {format: "https://gamefaqs.gamespot.com/company/%s-"},
	"anidb":          {format: "https://anidb.net/cr%s"},
	"vgmdb":          {format: "https://vgmdb.net/artist/%s"},
	"vgmdb_org":      {format: "https://vgmdb.net/org/%s"},
	"discogs":        {format: "https://www.discogs.com/artist/%s"},
	"mbrainz":        {format: "https://musicbrainz.org/artist/%s"},
	"imdb":           {format: "https://www.imdb.com/name/nm%07d", numeric: true},
	"kofi":           {format: "https://ko-fi.com/%s"},
	"deviantar":      {format: "https://www.deviantart.com/%s"},
	"mobygames":      {format: "https://www.mobygames.com/person/%s"},
	"anison":         {format: "http://anison.info/data/person/%s.html"},
}

// renderWeb builds the absolute URL for a site-native value, or ok=false when
// the value fails its sanity check. A failing value is counted and dropped —
// never repaired, never guessed into a URL that may not resolve.
func renderWeb(site, value string) (string, bool) {
	t, known := webTemplates[site]
	if !known {
		return "", false
	}
	v := strings.TrimSpace(value)
	if v == "" || strings.ContainsAny(v, " \t\r\n") {
		return "", false
	}
	if !t.allowSlash && strings.Contains(v, "/") {
		return "", false
	}
	if t.numeric {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return "", false
		}
		return fmt.Sprintf(t.format, n), true
	}
	return fmt.Sprintf(t.format, v), true
}

// ── lane × site matrix ──────────────────────────────────────────────────────

// siteSet is an allow-list of extlink sites: the sorted list feeds the SQL IN
// filter, the map answers the per-row dispatch.
type siteSet struct {
	list []string
	set  map[string]bool
}

func newSiteSet(sites ...string) siteSet {
	s := siteSet{set: make(map[string]bool, len(sites))}
	for _, site := range sites {
		if !s.set[site] {
			s.set[site] = true
			s.list = append(s.list, site)
		}
	}
	sort.Strings(s.list)
	return s
}

func (s siteSet) has(site string) bool { return s.set[site] }

// derive returns a copy with sites added and removed — the person web set is
// the label set with the two company-only spaces swapped for the credits ones.
func (s siteSet) derive(remove []string, add []string) siteSet {
	drop := make(map[string]bool, len(remove))
	for _, r := range remove {
		drop[r] = true
	}
	var sites []string
	for _, site := range s.list {
		if !drop[site] {
			sites = append(sites, site)
		}
	}
	return newSiteSet(append(sites, add...)...)
}

// The matrix. Anything not listed here is deliberately skipped — an unlisted
// site is either an identity space (vndb / bgmtv / egs_creator), a storefront
// already served by release-grain anchors, or a shape this job cannot render.
var (
	// Work grain, release chain: only the two the work itself owns. Every
	// store site on a release stays on the release.
	workTypedSites = newSiteSet("website", "twitter")
	// Work grain, vn chain: the three encyclopaedia spaces.
	workWebSites = newSiteSet("wikidata", "wp", "renai")

	labelTypedSites = newSiteSet("website", "twitter", "cien", "pixiv")
	labelWebSites   = newSiteSet(
		"youtube", "bsky", "instagram", "facebook", "tumblr", "fanbox", "fantia",
		"patreon", "scloud", "weibo", "bilibili", "nijie", "mobygames_comp",
		"gamefaqs_comp", "booth_pub", "itch_dev", "wikidata", "wp", "afdian",
		"boosty", "vk", "substar", "steam_curator")

	personTypedSites = labelTypedSites
	personWebSites   = labelWebSites.derive(
		[]string{"mobygames_comp", "gamefaqs_comp"},
		[]string{"anidb", "vgmdb", "vgmdb_org", "discogs", "mbrainz", "imdb",
			"kofi", "deviantar", "mobygames", "anison"})
)

// ── storefront guard ────────────────────────────────────────────────────────

// storeDomains are the marketplaces a work's "official site" must never point
// at: the work's store presence is anchored at RELEASE grain with a real store
// id, and a URL-shaped duplicate on the work would be the weaker twin of a
// stronger row.
var storeDomains = []string{
	"dlsite.com", "dmm.co.jp", "dmm.com", "getchu.com", "steampowered.com",
	"booth.pm", "itch.io", "freem.ne.jp", "novelgame.jp", "gyutto.com",
	"digiket.com", "toranoana.jp", "toranoana.shop", "melonbooks.co.jp",
	"melonbooks.com",
}

// isStoreHost reports whether a normalized URL (scheme already stripped) lives
// on a storefront. The suffix match is dot-bounded, so a subdomain
// (www.dlsite.com, ec.toranoana.jp, foo.itch.io) folds in while a look-alike
// host (notdlsite.com) does not.
func isStoreHost(normalizedURL string) bool {
	host := normalizedURL
	if i := strings.IndexAny(host, "/:?#"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	for _, d := range storeDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
