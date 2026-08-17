package handler

import (
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserWorkDetailIncludeHidden(t *testing.T) {
	db := openCatalogTestDB(t)
	work, liveID, hiddenID := seedHiddenReleaseWork(t, db)
	app := userVoteApp(db, userEditClients())
	token := userToken(t, 701, ScopeCatalogEdit, "kungal-client")

	status, raw := userVoteReq(t, app, "GET", fmt.Sprintf("%s/works/%d", UserPrefix, work), token)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	def := decodeUserWorkReleases(t, raw)
	require.Len(t, def, 1)
	assert.Equal(t, liveID, int64(def[0]["id"].(float64)))
	_, hasHidden := def[0]["hidden"]
	assert.False(t, hasHidden, "live rows omit hidden (omitempty) so the default payload stays byte-compatible")

	s2s := readApp(service.NewReadService(db), nil)
	s2sStatus, s2sBody := getJSON(t, s2s, fmt.Sprintf("/api/v1/catalog/works/%d", work))
	require.Equal(t, fiber.StatusOK, s2sStatus)
	s2sReleases := s2sBody["data"].(map[string]any)["releases"].([]any)
	require.Len(t, s2sReleases, 1)
	s2sItem := s2sReleases[0].(map[string]any)
	_, s2sHidden := s2sItem["hidden"]
	assert.False(t, s2sHidden, "S2S default payload must not grow a hidden key")

	status, raw = userVoteReq(t, app, "GET",
		fmt.Sprintf("%s/works/%d?include_hidden=true", UserPrefix, work), token)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	all := decodeUserWorkReleases(t, raw)
	require.Len(t, all, 2)
	byID := map[int64]map[string]any{}
	for _, item := range all {
		byID[int64(item["id"].(float64))] = item
	}
	assert.NotContains(t, byID[liveID], "hidden")
	assert.Equal(t, true, byID[hiddenID]["hidden"])
}

func decodeUserWorkReleases(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var env struct {
		Data struct {
			Releases []map[string]any `json:"releases"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	return env.Data.Releases
}

func seedHiddenReleaseWork(t *testing.T, db *gorm.DB) (workID, liveID, hiddenID int64) {
	t.Helper()
	for _, tbl := range []string{"catalog_external_ref", "catalog_release", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "隠蔽発見", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(&work).Error)
	live := model.CatalogRelease{WorkID: work.ID, Kind: model.ReleaseKindDefault, Extra: []byte(`{}`)}
	require.NoError(t, db.Create(&live).Error)
	hidden := model.CatalogRelease{WorkID: work.ID, Kind: model.ReleaseKindDigital, Extra: []byte(`{}`)}
	require.NoError(t, db.Create(&hidden).Error)
	require.NoError(t, db.Exec(`UPDATE catalog_release SET deleted_at = now() WHERE id = ?`, hidden.ID).Error)
	return work.ID, live.ID, hidden.ID
}
