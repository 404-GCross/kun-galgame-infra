package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"api/internal/middleware"
	"api/pkg/imageclient"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserEditSnapshot_IsAuthenticatedButNotFenced(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())
	path := fmt.Sprintf("%s/edit/snapshot?entity_type=catalog.work&entity_id=%d", UserPrefix, work)

	status, raw := userEditReq(t, app, "GET", path, "", "")
	assert.Equal(t, fiber.StatusUnauthorized, status, string(raw))

	status, raw = userEditReq(t, app, "GET", path,
		userToken(t, 601, "openid profile", "kungal-client"), "")
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	var env struct {
		Data struct {
			EntityType string         `json:"entity_type"`
			EntityID   int64          `json:"entity_id"`
			Values     map[string]any `json:"values"`
		} `json:"data"`
	}
	status, raw = userEditReq(t, app, "GET", path,
		userToken(t, 601, ScopeCatalogEdit, "kungal-client"), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	assert.Equal(t, "catalog.work", env.Data.EntityType)
	assert.Equal(t, work, env.Data.EntityID)
	assert.Equal(t, "利用者面テスト", env.Data.Values["catalog.work.display_name"])

	status, raw = userEditReq(t, app, "GET", path,
		userToken(t, 602, ScopeCatalogEdit, "letmoe-client"), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))

	status, _ = userEditReq(t, app, "GET",
		fmt.Sprintf("%s/edit/snapshot?entity_type=catalog.work&entity_id=%d", UserPrefix, 9_000_180),
		userToken(t, 601, ScopeCatalogEdit, "kungal-client"), "")
	assert.Equal(t, fiber.StatusNotFound, status)
	status, _ = userEditReq(t, app, "GET",
		fmt.Sprintf("%s/edit/snapshot?entity_type=galgame.game&entity_id=%d", UserPrefix, work),
		userToken(t, 601, ScopeCatalogEdit, "kungal-client"), "")
	assert.Equal(t, fiber.StatusNotFound, status)
}

type userListEnvelope struct {
	Data struct {
		Items []struct {
			ID          int64  `json:"id"`
			ProposerUID int64  `json:"proposer_uid"`
			Site        string `json:"site"`
			Status      string `json:"status"`
		} `json:"items"`
		Total int64 `json:"total"`
	} `json:"data"`
}

func userListProposals(t *testing.T, app *fiber.App, query, token string) (int, userListEnvelope, []byte) {
	t.Helper()
	status, raw := userEditReq(t, app, "GET", UserPrefix+"/edit/proposals"+query, token, "")
	var env userListEnvelope
	if status == fiber.StatusOK {
		require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	}
	return status, env, raw
}

func TestUserEditList_MineIsTheTokenAndNobodyElse(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	file := func(token, name string) int64 {
		status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals", token,
			userProposalBody(work, name, ""))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		return decodeUserCreate(t, raw).Data.Proposal.ID
	}
	mine := userToken(t, 611, ScopeCatalogEdit, "kungal-client")
	a := file(mine, "私の提案A")
	file(userToken(t, 612, ScopeCatalogEdit, "kungal-client"), "他人の提案")

	status, env, raw := userListProposals(t, app, "?entity_type=catalog.work&mine=true", mine)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	require.EqualValues(t, 1, env.Data.Total, "only this token's proposals, on this token's site")
	require.Len(t, env.Data.Items, 1)
	assert.Equal(t, a, env.Data.Items[0].ID)
	assert.EqualValues(t, 611, env.Data.Items[0].ProposerUID)
	assert.Equal(t, "kungal", env.Data.Items[0].Site)

	_, env, _ = userListProposals(t, app, "?entity_type=catalog.work&mine=true",
		userToken(t, 611, ScopeCatalogEdit, "letmoe-client"))
	assert.EqualValues(t, 0, env.Data.Total)
	assert.Empty(t, env.Data.Items)

	_, env, _ = userListProposals(t,
		app, "?entity_type=catalog.work&mine=true&proposer_uid=612&site=letmoe", mine)
	require.EqualValues(t, 1, env.Data.Total)
	assert.EqualValues(t, 611, env.Data.Items[0].ProposerUID)

	_, env, _ = userListProposals(t, app, "?entity_type=catalog.work&mine=true&status=open", mine)
	assert.EqualValues(t, 1, env.Data.Total)
	_, env, _ = userListProposals(t, app, "?entity_type=catalog.work&mine=true&status=merged", mine)
	assert.EqualValues(t, 0, env.Data.Total)
	status, _, _ = userListProposals(t, app, "?entity_type=catalog.work&mine=true&status=pending", mine)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
}

func TestUserEditList_QueueNeedsReviewAuthority(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, 621, ScopeCatalogEdit, "kungal-client"),
		userProposalBody(work, "査読待ちの提案", ""))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	status, _, raw = userListProposals(t, app, "?entity_type=catalog.work",
		userToken(t, 621, ScopeCatalogEdit, "kungal-client"))
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	status, env, raw := userListProposals(t, app, "?entity_type=catalog.work&mine=true",
		userToken(t, 621, ScopeCatalogEdit, "kungal-client"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 1, env.Data.Total)

	status, env, raw = userListProposals(t, app, "?entity_type=catalog.work",
		userTokenRoles(t, 900, ScopeCatalogEdit, "kungal-client", "user", "admin"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	require.EqualValues(t, 1, env.Data.Total)
	assert.EqualValues(t, 621, env.Data.Items[0].ProposerUID, "the queue is other people's work")

	status, _, _ = userListProposals(t, app, "?entity_type=catalog.work",
		userTokenRoles(t, 901, ScopeCatalogEdit, "kungal-client", "user", "moderator"))
	assert.Equal(t, fiber.StatusForbidden, status)

	status, _, _ = userListProposals(t, app, "?entity_type=galgame.game",
		userTokenRoles(t, 900, ScopeCatalogEdit, "kungal-client", "user", "admin"))
	assert.NotEqual(t, fiber.StatusOK, status)
}

func TestUserEditList_QueueAdmitsTheEntrysOwner(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditOwnedWork(t, db, 631)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, 632, ScopeCatalogEdit, "kungal-client"),
		userProposalBody(work, "他人からの提案", ""))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	scoped := fmt.Sprintf("?entity_type=catalog.work&entity_id=%d", work)
	status, env, raw := userListProposals(t, app, scoped,
		userToken(t, 631, ScopeCatalogEdit, "kungal-client"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 1, env.Data.Total)

	status, _, _ = userListProposals(t, app, "?entity_type=catalog.work",
		userToken(t, 631, ScopeCatalogEdit, "kungal-client"))
	assert.Equal(t, fiber.StatusForbidden, status)

	status, _, _ = userListProposals(t, app, scoped,
		userToken(t, 633, ScopeCatalogEdit, "kungal-client"))
	assert.Equal(t, fiber.StatusForbidden, status)
}

type userCoversEnvelope struct {
	Data struct {
		WorkID int64 `json:"work_id"`
		Covers []struct {
			ID        int64  `json:"id"`
			ImageHash string `json:"image_hash"`
			VoteCount int    `json:"vote_count"`
			Voted     bool   `json:"voted"`
		} `json:"covers"`
	} `json:"data"`
}

func userCoversPath(workID int64) string {
	return fmt.Sprintf("%s/works/%d/covers", UserPrefix, workID)
}

func TestUserCovers_VotedComesFromTheToken(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := userVoteApp(db, userEditClients())

	read := func(token, query string) userCoversEnvelope {
		t.Helper()
		status, raw := userVoteReq(t, app, "GET", userCoversPath(f.work)+query, token)
		require.Equal(t, fiber.StatusOK, status, string(raw))
		var env userCoversEnvelope
		require.NoError(t, json.Unmarshal(raw, &env), string(raw))
		return env
	}

	viewer := userToken(t, 641, ScopeCatalogEdit, "kungal-client")
	for _, token := range []string{viewer, userToken(t, 642, ScopeCatalogEdit, "kungal-client")} {
		status, raw := userVoteReq(t, app, "PUT", userVotePath(f.work, f.coverA), token)
		require.Equal(t, fiber.StatusOK, status, string(raw))
	}

	env := read(viewer, "")
	assert.Equal(t, f.work, env.Data.WorkID)
	require.Len(t, env.Data.Covers, 2, "every cover of the work, tally or not")
	byID := map[int64]int{}
	for i, cv := range env.Data.Covers {
		byID[cv.ID] = i
	}
	a, b := env.Data.Covers[byID[f.coverA]], env.Data.Covers[byID[f.coverB]]
	assert.Equal(t, 2, a.VoteCount)
	assert.True(t, a.Voted, "the viewer is the token's user")
	assert.NotEmpty(t, a.ImageHash)
	assert.Equal(t, 0, b.VoteCount)
	assert.False(t, b.Voted)

	third := read(userToken(t, 643, ScopeCatalogEdit, "kungal-client"), "?uid=641")
	assert.Equal(t, 2, third.Data.Covers[byID[f.coverA]].VoteCount)
	assert.False(t, third.Data.Covers[byID[f.coverA]].Voted,
		"the viewer is never taken from the wire")

	other := read(userToken(t, 644, ScopeCatalogEdit, "letmoe-client"), "")
	assert.Equal(t, 2, other.Data.Covers[byID[f.coverA]].VoteCount)

	status, raw := userVoteReq(t, app, "GET", userCoversPath(f.work), "")
	assert.Equal(t, fiber.StatusUnauthorized, status, string(raw))
	status, raw = userVoteReq(t, app, "GET", userCoversPath(9_000_180), viewer)
	assert.Equal(t, fiber.StatusNotFound, status, string(raw))
}

func userEditImagesApp(upload EditImageUpload) *fiber.App {
	app := fiber.New()
	verifier := oidctoken.NewVerifier(userTestSecret, nil)
	app.Use(UserPrefix, middleware.JWTAuth(verifier), UserGate(userEditClients()))
	SetupUserEditImages(app, upload)
	return app
}

func postUserEditImage(t *testing.T, app *fiber.App, token string, body *bytes.Buffer, contentType string) (*http.Response, Envelope[json.RawMessage]) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, UserPrefix+"/edit/images", body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	var env Envelope[json.RawMessage]
	_ = json.Unmarshal(raw, &env)
	return resp, env
}

func TestUserEditImageUpload_StampsTheTokenUser(t *testing.T) {
	var gotSub, gotPreset, gotFilename string
	var gotBytes []byte
	upload := func(_ context.Context, r io.Reader, filename, preset, sub string) (*imageclient.UploadResult, error) {
		gotBytes, _ = io.ReadAll(r)
		gotFilename, gotPreset, gotSub = filename, preset, sub
		return &imageclient.UploadResult{Hash: "abc", URL: "https://cdn/x.webp"}, nil
	}
	app := userEditImagesApp(upload)

	body, ct := multipartBody(t, "galgame_banner", "999", true)
	resp, env := postUserEditImage(t, app, userToken(t, 651, ScopeCatalogEdit, "kungal-client"), body, ct)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.EqualValues(t, 0, env.Code, env.Message)
	assert.Equal(t, "kungal:651", gotSub, "the uploader is the token's id claim")
	assert.Equal(t, "catalog_cover", gotPreset, "the wire preset maps to the catalog site's own")
	assert.Equal(t, "cover.png", gotFilename)
	assert.Equal(t, "png-bytes", string(gotBytes))
}

func TestUserEditImageUpload_BehindTheSameGate(t *testing.T) {
	called := false
	app := userEditImagesApp(func(context.Context, io.Reader, string, string, string) (*imageclient.UploadResult, error) {
		called = true
		return nil, nil
	})

	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"no token at all", "", http.StatusUnauthorized},
		{"token without the catalog:edit scope",
			userToken(t, 5, "openid profile", "kungal-client"), http.StatusForbidden},
		{"first-party token: no client binding",
			userToken(t, 5, ScopeCatalogEdit, ""), http.StatusForbidden},
	} {
		body, ct := multipartBody(t, "galgame_banner", "", true)
		resp, _ := postUserEditImage(t, app, tc.token, body, ct)
		assert.Equalf(t, tc.want, resp.StatusCode, tc.name)
	}
	assert.False(t, called, "no refusal reached the image service")

	body, ct := multipartBody(t, "avatar", "", true)
	resp, _ := postUserEditImage(t, app, userToken(t, 652, ScopeCatalogEdit, "kungal-client"), body, ct)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.False(t, called)
}

func multipartBody(t *testing.T, preset, actorUID string, withFile bool) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if preset != "" {
		require.NoError(t, mw.WriteField("preset", preset))
	}
	if actorUID != "" {
		require.NoError(t, mw.WriteField("actor_uid", actorUID))
	}
	if withFile {
		fw, err := mw.CreateFormFile("file", "cover.png")
		require.NoError(t, err)
		_, err = fw.Write([]byte("png-bytes"))
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	return body, mw.FormDataContentType()
}

func TestUserEditImageUpload_HandlerRefusals(t *testing.T) {
	tok := userToken(t, 653, ScopeCatalogEdit, "kungal-client")

	app := userEditImagesApp(func(context.Context, io.Reader, string, string, string) (*imageclient.UploadResult, error) {
		t.Fatal("must not be called")
		return nil, nil
	})
	body, ct := multipartBody(t, "galgame_screenshot", "", false)
	resp, env := postUserEditImage(t, app, tok, body, ct)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.NotEqualValues(t, 0, env.Code)

	body, ct = multipartBody(t, "galgame_banner", "", true)
	resp, env = postUserEditImage(t, userEditImagesApp(nil), tok, body, ct)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.NotEqualValues(t, 0, env.Code)

	quota := userEditImagesApp(func(context.Context, io.Reader, string, string, string) (*imageclient.UploadResult, error) {
		return nil, fmt.Errorf("wrap: %w", imageclient.ErrQuotaExceeded)
	})
	body, ct = multipartBody(t, "galgame_banner", "", true)
	resp, env = postUserEditImage(t, quota, tok, body, ct)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.NotEqualValues(t, 0, env.Code)
}
