package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	apierrors "api/pkg/errors"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func getRaw(t *testing.T, app *fiber.App, url string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func seedMergedLabels(t *testing.T, db *gorm.DB) (live, mid, gone int64) {
	t.Helper()
	for _, tbl := range []string{"catalog_redirect", "catalog_work_label", "catalog_label"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name string) int64 {
		l := model.CatalogLabel{DisplayName: name, Kind: model.LabelKindGameBrand}
		require.NoError(t, db.Create(&l).Error)
		return l.ID
	}
	live, mid, gone = mk("生存ブランド"), mk("中間ブランド"), mk("ももいろたんざく")

	require.NoError(t, repository.InsertRedirect(db, model.EntityTypeLabel, gone, mid, nil, "test merge 1"))
	require.NoError(t, db.Delete(&model.CatalogLabel{}, gone).Error)
	require.NoError(t, repository.FlattenRedirectsTo(db, model.EntityTypeLabel, mid, live))
	require.NoError(t, repository.InsertRedirect(db, model.EntityTypeLabel, mid, live, nil, "test merge 2"))
	require.NoError(t, db.Delete(&model.CatalogLabel{}, mid).Error)
	return live, mid, gone
}

func TestPublicLabelDetailMergedRedirects(t *testing.T) {
	db := openCatalogTestDB(t)
	live, mid, gone := seedMergedLabels(t, db)
	app := publicApp(db)

	t.Run("live label is untouched", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(live))
		require.Equal(t, 200, resp.StatusCode)
		data := body["data"].(map[string]any)
		assert.EqualValues(t, live, data["id"])
		assert.Equal(t, "生存ブランド", data["display_name"])
	})

	t.Run("merged label redirects with current_id", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(mid))
		require.Equal(t, 301, resp.StatusCode)
		assert.Equal(t, "/v1/catalog/labels/"+itoa(live), resp.Header.Get("Location"))
		assert.EqualValues(t, apierrors.ErrMoved, body["code"])
		data := body["data"].(map[string]any)
		assert.Equal(t, "label", data["entity_type"])
		assert.EqualValues(t, mid, data["id"])
		assert.EqualValues(t, live, data["current_id"])
		assert.NotContains(t, data, "display_name")
	})

	t.Run("chained merge resolves to the final survivor", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(gone))
		require.Equal(t, 301, resp.StatusCode)
		assert.Equal(t, "/v1/catalog/labels/"+itoa(live), resp.Header.Get("Location"))
		data := body["data"].(map[string]any)
		assert.EqualValues(t, live, data["current_id"], "must be the final id, not the middle one")
	})

	t.Run("absent id is a plain 404", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(live+9999))
		require.Equal(t, 404, resp.StatusCode)
		assert.EqualValues(t, apierrors.ErrNotFound, body["code"])
	})

	t.Run("soft-deleted with no redirect is a 404, not a redirect", func(t *testing.T) {
		orphan := model.CatalogLabel{DisplayName: "孤児ブランド", Kind: model.LabelKindGameBrand}
		require.NoError(t, db.Create(&orphan).Error)
		require.NoError(t, db.Delete(&model.CatalogLabel{}, orphan.ID).Error)
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(orphan.ID))
		require.Equal(t, 404, resp.StatusCode)
		assert.EqualValues(t, apierrors.ErrNotFound, body["code"])
	})
}
