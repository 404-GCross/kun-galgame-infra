package vndb

import "testing"

func TestIsStore(t *testing.T) {
	// Kept: stores, console shops, official website. (VNDB site `name`s.)
	for _, name := range []string{
		"website", "steam", "dmm", "getchu", "dlsite", "jastusa", "mg",
		"johren", "toranoana", "melonjp", "melon", "playasia", "gog", "denpa",
		"booth", "itch", "nintendo_jp", "playstation_eu", "fakku", "kagura",
		"nutaku", "gamejolt", "patreon", "substar", "googplay", "appstore",
	} {
		if !isStore(name) {
			t.Errorf("site %q should be KEPT (store/official) but was dropped", name)
		}
	}
	// Dropped: info / stats / aggregator / encyclopedic, and the auto-fetched
	// extras VNDB returns next to real stores. Note VNDB's suffixes.
	for _, name := range []string{
		"egs", "igdb_game", "mobygames_game", "acdb_source", "anidb",
		"vgmdb_product", "enwiki", "jawiki", "zhwiki", "wikidata", "steamdb",
		"gogdb", "lutris", "gamefaqs_game", "howlongtobeat", "pcgamingwiki",
		"wine", "renai", "vndb",
	} {
		if isStore(name) {
			t.Errorf("site %q should be DROPPED (info/stats/extra) but was kept", name)
		}
	}
}
