// works_titles_test.go — A2-R1: the reindexer's title loader.
//
// The read face is what a consumer sees; this is what the SEARCH index sees, and
// the wave exists because the two had drifted apart in the worst direction: a
// claimed work's names live in the wiki body, the loader read only
// catalog_work_title, and so tens of thousands of claimed works were unfindable
// by their own Chinese name. The cases here are the read face's rules — the
// claimed bridge with its intra-bridge alias dedup, strict XOR on the display
// lane, bodyless works verbatim, nothing outside the index population — plus the
// one deliberate difference: search hints are a catalog-native findability lane
// with no wiki counterpart, so a claimed work keeps them.
package main

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// titlesOf flattens the loader's result for one work into (lang, title, kind).
func titlesOf(t *testing.T, workID int64) [][3]any {
	t.Helper()
	all, err := loadWorkTitles(facetTestDB)
	if err != nil {
		t.Fatalf("loadWorkTitles: %v", err)
	}
	out := make([][3]any, 0, len(all[workID]))
	for _, r := range all[workID] {
		out = append(out, [3]any{r.lang, r.title, r.kind})
	}
	return out
}

// TestLoadWorkTitlesMirrorsTheReadFace covers the whole rule set in one corpus.
func TestLoadWorkTitlesMirrorsTheReadFace(t *testing.T) {
	truncateFacetTables(t)
	wikiIDs := []int64{97001, 97002}
	ensureGalgameBodyStub(t, facetTestDB, wikiIDs)
	t.Cleanup(func() {
		_ = facetTestDB.Exec(`DELETE FROM galgame_alias WHERE galgame_id IN ?`, wikiIDs).Error
		_ = facetTestDB.Exec(`DELETE FROM galgame WHERE id IN ?`, wikiIDs).Error
	})

	// ── claimed: bridges the wiki body's four name columns + its aliases ──
	claimed := mkWork(t, "claimed-title", model.WorkStatusLive, galgameMedium)
	if err := facetTestDB.Exec(
		`UPDATE catalog_work SET site = 'galgame_wiki', product_work_id = ? WHERE id = ?`,
		wikiIDs[0], claimed).Error; err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := facetTestDB.Exec(`INSERT INTO galgame (id, user_id, name_ja_jp, name_en_us, name_zh_cn, name_zh_tw)
		VALUES (?, 1, '日本語名', 'English Name', '简体中文名', '  ')`, wikiIDs[0]).Error; err != nil {
		t.Fatalf("seed galgame body: %v", err)
	}
	for _, a := range []string{"べつめい", "日本語名", "  "} {
		if err := facetTestDB.Exec(
			`INSERT INTO galgame_alias (galgame_id, name) VALUES (?, ?)`, wikiIDs[0], a).Error; err != nil {
			t.Fatalf("seed galgame alias: %v", err)
		}
	}
	// A native DISPLAY row on the claimed work is invisible (strict XOR); its
	// native SEARCH HINT is not (the DLsite product name lane the wiki lacks).
	for _, r := range []struct {
		lang, title string
		kind        int16
	}{
		{"ja", "この行は使われない", model.WorkTitleKindOfficial},
		{"ja", "ディーエルサイト名", model.WorkTitleKindSearchHint},
	} {
		if err := facetTestDB.Exec(`INSERT INTO catalog_work_title (work_id, lang, title, kind)
			VALUES (?, ?, ?, ?)`, claimed, r.lang, r.title, r.kind).Error; err != nil {
			t.Fatalf("seed native title: %v", err)
		}
	}

	// ── bodyless: reads the native table, every kind, verbatim ──
	bodyless := mkWork(t, "bodyless-title", model.WorkStatusLive, galgameMedium)
	for _, r := range []struct {
		lang, title string
		kind        int16
	}{
		{"ja", "無体作品", model.WorkTitleKindOfficial},
		{"", "むたい", model.WorkTitleKindAlias},
		{"ja", "けんさくヒント", model.WorkTitleKindSearchHint},
	} {
		if err := facetTestDB.Exec(`INSERT INTO catalog_work_title (work_id, lang, title, kind)
			VALUES (?, ?, ?, ?)`, bodyless, r.lang, r.title, r.kind).Error; err != nil {
			t.Fatalf("seed native title: %v", err)
		}
	}

	// ── outside the population: a stub work contributes nothing ──
	stub := mkWork(t, "stub-title", model.WorkStatusStub, galgameMedium)
	if err := facetTestDB.Exec(`INSERT INTO catalog_work_title (work_id, lang, title, kind)
		VALUES (?, 'ja', 'スタブ', 0)`, stub).Error; err != nil {
		t.Fatalf("seed stub title: %v", err)
	}

	got := titlesOf(t, claimed)
	want := [][3]any{
		{"ja", "日本語名", model.WorkTitleKindOfficial},
		{"en", "English Name", model.WorkTitleKindOfficial},
		{"zh-Hans", "简体中文名", model.WorkTitleKindOfficial},
		{"", "べつめい", model.WorkTitleKindAlias},
		{"ja", "ディーエルサイト名", model.WorkTitleKindSearchHint},
	}
	if len(got) != len(want) {
		t.Fatalf("claimed titles = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claimed titles[%d] = %v, want %v (whole set %v)", i, got[i], want[i], got)
		}
	}

	got = titlesOf(t, bodyless)
	if len(got) != 3 {
		t.Fatalf("bodyless titles = %v, want all three native rows verbatim", got)
	}
	if got[0] != ([3]any{"ja", "無体作品", model.WorkTitleKindOfficial}) {
		t.Fatalf("bodyless titles[0] = %v", got[0])
	}
	if got[2] != ([3]any{"ja", "けんさくヒント", model.WorkTitleKindSearchHint}) {
		t.Fatalf("bodyless keeps its search hint: %v", got)
	}

	if got = titlesOf(t, stub); len(got) != 0 {
		t.Fatalf("a non-LIVE work contributed titles: %v", got)
	}
}
