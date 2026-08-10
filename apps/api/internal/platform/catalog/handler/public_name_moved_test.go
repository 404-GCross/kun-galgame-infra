package handler

import (
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/srcbangumi"
	apierrors "api/pkg/errors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicNameDetailMergedRedirects(t *testing.T) {
	db := openCatalogTestDB(t)
	require.NoError(t, srcbangumi.EnsureSchema(db))
	for _, tbl := range []string{"catalog_redirect", "catalog_credit", "catalog_name_alias", "catalog_credit_name"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name string) int64 {
		n := model.CatalogCreditName{Name: name}
		require.NoError(t, db.Create(&n).Error)
		return n.ID
	}
	live, gone := mk("種﨑敦美"), mk("種﨑 敦美")
	require.NoError(t, repository.InsertRedirect(db, model.EntityTypeCreditName, gone, live, nil, "test fold"))
	require.NoError(t, db.Exec("DELETE FROM catalog_credit_name WHERE id = ?", gone).Error)
	app := publicApp(db)

	t.Run("live name is untouched", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/names/"+itoa(live))
		require.Equal(t, 200, resp.StatusCode)
		data := body["data"].(map[string]any)
		assert.EqualValues(t, live, data["id"])
	})

	t.Run("merged name redirects with current_id", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/names/"+itoa(gone))
		require.Equal(t, 301, resp.StatusCode)
		assert.Equal(t, "/v1/catalog/names/"+itoa(live), resp.Header.Get("Location"))
		assert.EqualValues(t, apierrors.ErrMoved, body["code"])
		data := body["data"].(map[string]any)
		assert.Equal(t, "name", data["entity_type"])
		assert.EqualValues(t, live, data["current_id"])
	})

	t.Run("absent id is a plain 404", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/names/"+itoa(live+9999))
		require.Equal(t, 404, resp.StatusCode)
		assert.EqualValues(t, apierrors.ErrNotFound, body["code"])
	})
}

func TestPublicCharacterDetailMergedRedirects(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_redirect", "catalog_character"} {
		require.NoError(t, db.Exec("TRUNCATE " + tbl + " RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name string) int64 {
		ch := model.CatalogCharacter{DisplayName: name}
		require.NoError(t, db.Create(&ch).Error)
		return ch.ID
	}
	live, gone := mk("冬月十夜"), mk("冬月 十夜")
	require.NoError(t, repository.InsertRedirect(db, model.EntityTypeCharacter, gone, live, nil, "test merge"))
	require.NoError(t, db.Delete(&model.CatalogCharacter{}, gone).Error)
	app := publicApp(db)

	t.Run("merged character redirects with current_id", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/characters/"+itoa(gone))
		require.Equal(t, 301, resp.StatusCode)
		assert.Equal(t, "/v1/catalog/characters/"+itoa(live), resp.Header.Get("Location"))
		data := body["data"].(map[string]any)
		assert.EqualValues(t, live, data["current_id"])
	})

	t.Run("absent id is a plain 404", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/characters/"+itoa(live+9999))
		require.Equal(t, 404, resp.StatusCode)
		assert.EqualValues(t, apierrors.ErrNotFound, body["code"])
	})
}
