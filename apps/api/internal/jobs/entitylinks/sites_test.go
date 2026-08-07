package entitylinks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRenderWeb pins the template table's contract: a site-native value becomes
// exactly one URL, and a value that is not the bare id the template expects is
// refused rather than repaired.
func TestRenderWeb(t *testing.T) {
	cases := []struct {
		site, value, want string
		ok                bool
	}{
		{site: "wikidata", value: "12345", want: "https://www.wikidata.org/wiki/Q12345", ok: true},
		{site: "tumblr", value: "someone", want: "https://someone.tumblr.com/", ok: true},
		{site: "imdb", value: "1234", want: "https://www.imdb.com/name/nm0001234", ok: true},
		{site: "imdb", value: "12345678", want: "https://www.imdb.com/name/nm12345678", ok: true},
		{site: "anison", value: "10", want: "http://anison.info/data/person/10.html", ok: true},
		// substar is the one template whose value legitimately carries a path.
		{site: "substar", value: "adult/name", want: "https://subscribestar.adult/name", ok: true},
		// refusals — never guessed into a URL that may not resolve.
		{site: "imdb", value: "nm0001234"},
		{site: "wp", value: "Some Title"},
		{site: "wp", value: "a/b"},
		{site: "kofi", value: "  "},
		{site: "dlsite", value: "RJ123456"}, // not in the table at all
	}
	for _, c := range cases {
		got, ok := renderWeb(c.site, c.value)
		assert.Equal(t, c.ok, ok, "%s=%q", c.site, c.value)
		assert.Equal(t, c.want, got, "%s=%q", c.site, c.value)
	}
}

func TestIsStoreHost(t *testing.T) {
	stores := []string{
		"www.dlsite.com/maniax/work/=/product_id/RJ01.html",
		"dmm.co.jp/foo", "store.steampowered.com/app/1", "getchu.com/soft.phtml",
		"someone.booth.pm", "someone.itch.io/game", "ec.toranoana.jp/x",
		"www.melonbooks.co.jp/x", "gyutto.com/i/item1", "digiket.com/x",
		"www.freem.ne.jp/win/game/1", "novelgame.jp/games/show/1",
	}
	for _, s := range stores {
		assert.True(t, isStoreHost(s), s)
	}
	for _, s := range []string{"alicesoft.com", "windmill.suki.jp", "notdlsite.com/x", "example.com/dlsite.com"} {
		assert.False(t, isStoreHost(s), s)
	}
}

// TestPersonWebSites locks the lane matrix's one derived set: the person lane
// is the label lane minus the two company-only spaces plus the credits ones,
// and it never carries an identity space.
func TestPersonWebSites(t *testing.T) {
	for _, s := range []string{"mobygames_comp", "gamefaqs_comp"} {
		assert.False(t, personWebSites.has(s), s)
		assert.True(t, labelWebSites.has(s), s)
	}
	for _, s := range []string{"anidb", "vgmdb", "vgmdb_org", "discogs", "mbrainz", "imdb", "kofi", "deviantar", "mobygames", "anison"} {
		assert.True(t, personWebSites.has(s), s)
		assert.False(t, labelWebSites.has(s), s)
	}
	assert.True(t, personWebSites.has("wikidata"), "shared spaces survive the derivation")
	for _, s := range []string{"vndb", "bgmtv", "egs_creator", "dlsite", "steam"} {
		assert.False(t, personWebSites.has(s), s)
		assert.False(t, labelWebSites.has(s), s)
		assert.False(t, workWebSites.has(s), s)
	}
	// Every allowed web site must be renderable, or the lane would count it
	// as malformed forever.
	for _, set := range []siteSet{workWebSites, labelWebSites, personWebSites} {
		for _, s := range set.list {
			_, known := webTemplates[s]
			assert.True(t, known, "no template for %q", s)
		}
	}
}
