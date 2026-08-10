package entitylinks

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

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

type webTemplate struct {
	format     string
	allowSlash bool
	numeric    bool
}

var webTemplates = map[string]webTemplate{
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

var (
	workTypedSites = newSiteSet("website", "twitter")
	workWebSites   = newSiteSet("wikidata", "wp", "renai")

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

var storeDomains = []string{
	"dlsite.com", "dmm.co.jp", "dmm.com", "getchu.com", "steampowered.com",
	"booth.pm", "itch.io", "freem.ne.jp", "novelgame.jp", "gyutto.com",
	"digiket.com", "toranoana.jp", "toranoana.shop", "melonbooks.co.jp",
	"melonbooks.com",
}

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
