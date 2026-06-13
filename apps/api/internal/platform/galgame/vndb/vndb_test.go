package vndb

import "testing"

func TestDenied(t *testing.T) {
	// Kept: stores, console shops, official website.
	for _, name := range []string{
		"steam", "dmm", "getchu", "dlsite", "dlsiteen", "jastusa", "mg",
		"johren", "toranoana", "melonjp", "playasia", "gog", "denpa",
		"booth", "itch", "nintendo_jp", "playstation_eu", "website",
	} {
		if denied(name) {
			t.Errorf("site %q should be KEPT (store/official) but was denied", name)
		}
	}
	// Dropped: info / stats / aggregator / encyclopedic. Note VNDB's suffixes.
	for _, name := range []string{
		"igdb_game", "mobygames_game", "acdb_source", "anidb", "vgmdb_product",
		"enwiki", "jawiki", "zhwiki", "wikidata", "egs", "steamdb",
		"howlongtobeat", "renai", "vndb",
	} {
		if !denied(name) {
			t.Errorf("site %q should be DENIED (info/stats) but was kept", name)
		}
	}
}
