// public_titles_test.go — A2-R1 区 A wire-level cases (refs/proj/136): the
// claimed work's TITLE face end to end.
//
// Originally an A/B harness over the wiki title bridge, then over the wave-140
// mirror that replaced it. Wave 161 retired both the wiki galgame / galgame_alias
// layer and the mirror, so these are now plain CONTRACT tests over native
// catalog_work_title rows: the fixtures below ARE the rows the mirror produced
// for the old wiki bodies, which is why the frozen expectations never moved.
//
// The regression the original wave fixed: 87% of claimed works had ZERO
// catalog_work_title rows, so their Chinese names and aliases were absent from
// every consumer. The cases pin the resulting shape — the four-key pivot, aliases
// as lang-less alias rows, and byte-identity for a bodyless work.
package handler

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// titleRows flattens a work record's titles[] into comparable tuples.
func titleRows(t *testing.T, body map[string]any) [][3]string {
	t.Helper()
	raw, ok := body["data"].(map[string]any)["titles"].([]any)
	require.True(t, ok, "titles[] must be present")
	out := make([][3]string, 0, len(raw))
	for _, r := range raw {
		m := r.(map[string]any)
		out = append(out, [3]string{m["lang"].(string), m["title"].(string), m["kind"].(string)})
	}
	return out
}

// TestClaimedWorkTitlesBridge is 区 A's core case on the PUBLIC work record, now a
// CONTRACT test over NATIVE catalog_work_title rows: the wiki title layer and its
// mirror are both gone (wave 161), so the claimed work is seeded with exactly the
// rows the wave-140 mirror produced for this fixture — the four name columns
// pivoted to BCP-47 official rows and the one surviving galgame_alias as a
// lang-less alias row. A bodyless work reads its native rows unchanged.
//
// The frozen expectations below are therefore unchanged from the bridge era; only
// the INPUT moved from "wiki body + mirror" to "the mirror's own output, written
// directly". The detail lane sorts (kind, lang) like every native work's.
func TestClaimedWorkTitlesBridge(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	// ── CLAIMED: the mirror's output for a body carrying all four name columns
	// plus three aliases — one repeating the official ja name (dropped: one name,
	// one row) and one whitespace-only (not an alias). Only べつめい survived, with
	// no language and latin NULL.
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "認領作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5001)}
	require.NoError(t, db.Create(&claimed).Error)
	for _, row := range []model.CatalogWorkTitle{
		{WorkID: claimed.ID, Lang: "ja", Title: "日本語名", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Lang: "en", Title: "English Name", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Lang: "zh-Hans", Title: "简体中文名", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Lang: "zh-Hant", Title: "繁體中文名", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Title: "べつめい", Kind: model.WorkTitleKindAlias},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	// The bridge-era "the mirror DELETES a stale native row the wiki body does not
	// justify" sub-case and its fixture row are gone: with no mirror there is no
	// deletion arm to exercise, and catalog_work_title is now the only truth there
	// ever was. Every response assertion below is unchanged.

	// ── BODYLESS: native rows, verbatim (including latin, excluding search hints).
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	latin := "Mutai Sakuhin"
	require.NoError(t, db.Create(&model.CatalogWorkTitle{
		WorkID: bodyless.ID, Lang: "ja", Title: "無体作品", Latin: &latin, Kind: model.WorkTitleKindOfficial,
	}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTitle{
		WorkID: bodyless.ID, Lang: "ja", Title: "けんさくヒント", Kind: model.WorkTitleKindSearchHint,
	}).Error)

	app := supplyApp(db)

	// CLAIMED: the four official rows — (kind, lang) ordered on the detail lane —
	// then the one surviving alias, lang-less, kind=alias.
	code, body := getJSON(t, app, "/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	assert.Equal(t, [][3]string{
		{"en", "English Name", "official"},
		{"ja", "日本語名", "official"},
		{"zh-Hans", "简体中文名", "official"},
		{"zh-Hant", "繁體中文名", "official"},
		{"", "べつめい", "alias"},
	}, titleRows(t, body), "one row per name, (kind, lang) ordered")

	// BODYLESS: unchanged — the official row with its latin, no search hint.
	code, body = getJSON(t, app, "/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	assert.Equal(t, [][3]string{{"ja", "無体作品", "official"}}, titleRows(t, body),
		"a bodyless work still reads its native rows, search hints excluded")
	first := body["data"].(map[string]any)["titles"].([]any)[0].(map[string]any)
	assert.Equal(t, "Mutai Sakuhin", first["latin"], "native latin survives the shared row shape")
}

// TestClaimedWorkNamesBlockBridged pins the LIST face's D7 names pivot on top of
// the NATIVE title rows: the four product keys all fill from the four official
// rows, and the lang-less alias row stays OUT of the block (it carries no
// language, so it belongs to no key).
//
// The fixture is the wave-140 mirror's output for a body with all four name
// columns plus one alias — the numbers are the frozen ones, only the input moved.
func TestClaimedWorkNamesBlockBridged(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "認領一覧", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5002)}
	require.NoError(t, db.Create(&claimed).Error)
	for _, row := range []model.CatalogWorkTitle{
		{WorkID: claimed.ID, Lang: "ja", Title: "日本語名", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Lang: "en", Title: "English Name", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Lang: "zh-Hans", Title: "简体中文名", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Lang: "zh-Hant", Title: "繁體中文名", Kind: model.WorkTitleKindOfficial},
		{WorkID: claimed.ID, Title: "べつめい", Kind: model.WorkTitleKindAlias},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	app := supplyApp(db)
	code, body := getJSON(t, app, "/v1/catalog/works?include=names")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	names := items[0].(map[string]any)["names"].(map[string]any)
	assert.Equal(t, "日本語名", names["ja-jp"])
	assert.Equal(t, "English Name", names["en-us"])
	assert.Equal(t, "简体中文名", names["zh-cn"])
	assert.Equal(t, "繁體中文名", names["zh-tw"])
	assert.Len(t, names, 4, "a lang-less alias belongs to no product key")
}
