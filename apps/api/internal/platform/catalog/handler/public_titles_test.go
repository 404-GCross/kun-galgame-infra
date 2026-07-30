// public_titles_test.go — A2-R1 区 A wire-level cases (refs/proj/136): the
// CLAIMED title bridge end to end.
//
// It lives here rather than in the service package for the same reason the tag
// safety axis does: the bridge reads the wiki-side galgame / galgame_alias
// layer, whose stubs (ensureGalgameStub) are defined in read_test.go.
//
// The regression this wave fixes: 87% of claimed works had ZERO
// catalog_work_title rows, so their Chinese names and aliases were absent from
// every consumer. The cases below pin the fix and its guardrails — the four-key
// pivot, aliases as lang-less alias rows, strict XOR over a shadow native row,
// and byte-identity for a bodyless work.
package handler

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertGalgameNames fills one fixture body's four name columns (the bridge's
// official lane). user_id is passed for the real-schema case.
func insertGalgameNames(t *testing.T, db *gorm.DB, id int64, ja, en, zhCN, zhTW string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame (id, user_id, name_ja_jp, name_en_us, name_zh_cn, name_zh_tw,
		intro_ja_jp, intro_en_us, intro_zh_cn, intro_zh_tw)
		VALUES (?, 0, ?, ?, ?, ?, '', '', '', '')`, id, ja, en, zhCN, zhTW).Error)
}

// insertGalgameAlias appends one wiki alias row (the bridge's alias lane).
func insertGalgameAlias(t *testing.T, db *gorm.DB, galgameID int64, name string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO galgame_alias (galgame_id, name) VALUES (?, ?)`, galgameID, name).Error)
}

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

// TestClaimedWorkTitlesBridge is 区 A's core case on the PUBLIC work record:
// a claimed work's titles come from the wiki body (four name columns pivoted to
// BCP-47 official rows + galgame_alias as lang-less alias rows), a shadow native
// row never surfaces (strict XOR), an alias that repeats an official name is not
// rendered twice, and a bodyless work reads its native rows unchanged.
func TestClaimedWorkTitlesBridge(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameCoverStub(t, db)
	ensureGalgameScreenshotStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	// ── CLAIMED: all four name columns + three aliases, one of which repeats an
	// official name and one of which is whitespace-only.
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "認領作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5001)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameNames(t, db, 5001, "日本語名", "English Name", "简体中文名", "繁體中文名")
	insertGalgameAlias(t, db, 5001, "べつめい")
	insertGalgameAlias(t, db, 5001, "日本語名") // == the official ja name: one name, one row
	insertGalgameAlias(t, db, 5001, "   ")  // whitespace is not an alias
	// A SHADOW native row on the same claimed work must never surface.
	require.NoError(t, db.Create(&model.CatalogWorkTitle{
		WorkID: claimed.ID, Lang: "ja", Title: "この行は使われない", Kind: model.WorkTitleKindOfficial,
	}).Error)

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

	// CLAIMED: the four pivot rows in fixed table-position order, then the one
	// surviving alias — lang-less, kind=alias.
	code, body := getJSON(t, app, "/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	assert.Equal(t, [][3]string{
		{"ja", "日本語名", "official"},
		{"en", "English Name", "official"},
		{"zh-Hans", "简体中文名", "official"},
		{"zh-Hant", "繁體中文名", "official"},
		{"", "べつめい", "alias"},
	}, titleRows(t, body), "bridge = four-key pivot then aliases; XOR drops the shadow native row")

	// BODYLESS: unchanged — the official row with its latin, no search hint.
	code, body = getJSON(t, app, "/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	assert.Equal(t, [][3]string{{"ja", "無体作品", "official"}}, titleRows(t, body),
		"a bodyless work still reads its native rows, search hints excluded")
	first := body["data"].(map[string]any)["titles"].([]any)[0].(map[string]any)
	assert.Equal(t, "Mutai Sakuhin", first["latin"], "native latin survives the shared row shape")
}

// TestClaimedWorkNamesBlockBridged pins the LIST face's D7 names pivot on top of
// the bridge: the four product keys all fill from the wiki body, and the
// lang-less alias rows stay OUT of the block (they carry no language, so they
// belong to no key — which is exactly why the bridge does not invent one).
func TestClaimedWorkNamesBlockBridged(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameCoverStub(t, db)
	ensureGalgameScreenshotStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "認領一覧", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5002)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameNames(t, db, 5002, "日本語名", "English Name", "简体中文名", "繁體中文名")
	insertGalgameAlias(t, db, 5002, "べつめい")

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
