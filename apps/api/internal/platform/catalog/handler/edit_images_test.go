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

	"api/pkg/imageclient"

	"github.com/gofiber/fiber/v3"
)

// The upload leg is a plain Fiber route (spec-invisible by design), so it is
// tested at the HTTP layer: multipart in, house envelope out.

func editImagesApp(upload EditImageUpload) *fiber.App {
	app := fiber.New()
	SetupEditImages(app, upload)
	return app
}

func multipartBody(t *testing.T, preset, actorUID string, withFile bool) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	if preset != "" {
		if err := mw.WriteField("preset", preset); err != nil {
			t.Fatal(err)
		}
	}
	if actorUID != "" {
		if err := mw.WriteField("actor_uid", actorUID); err != nil {
			t.Fatal(err)
		}
	}
	if withFile {
		fw, err := mw.CreateFormFile("file", "cover.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte("png-bytes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return body, mw.FormDataContentType()
}

func postEditImage(t *testing.T, app *fiber.App, body *bytes.Buffer, contentType string) (*http.Response, Envelope[json.RawMessage]) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/edit/images", body)
	req.Header.Set("Content-Type", contentType)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var env Envelope[json.RawMessage]
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("malformed envelope %q: %v", raw, err)
	}
	return resp, env
}

func TestEditImageUploadForwardsFileAndStampsActor(t *testing.T) {
	var gotPreset, gotSub, gotFilename string
	var gotBytes []byte
	app := editImagesApp(func(_ context.Context, r io.Reader, filename, preset, sub string) (*imageclient.UploadResult, error) {
		gotBytes, _ = io.ReadAll(r)
		gotFilename, gotPreset, gotSub = filename, preset, sub
		return &imageclient.UploadResult{Hash: "abc", URL: "https://cdn/x.webp", Width: 3, Height: 4}, nil
	})

	body, ct := multipartBody(t, "galgame_banner", "42", true)
	resp, env := postEditImage(t, app, body, ct)
	if resp.StatusCode != http.StatusOK || env.Code != 0 {
		t.Fatalf("status=%d code=%d msg=%s", resp.StatusCode, env.Code, env.Message)
	}
	var data imageclient.UploadResult
	if err := json.Unmarshal(env.Data, &data); err != nil || data.Hash != "abc" {
		t.Fatalf("data=%s err=%v", env.Data, err)
	}
	if gotPreset != "galgame_banner" || gotFilename != "cover.png" || string(gotBytes) != "png-bytes" {
		t.Fatalf("forwarded preset=%q filename=%q bytes=%q", gotPreset, gotFilename, gotBytes)
	}
	if gotSub != "kungal:42" {
		t.Fatalf("uploader sub = %q, want kungal:42", gotSub)
	}
}

func TestEditImageUploadRejectsUnknownPresetBeforeForwarding(t *testing.T) {
	called := false
	app := editImagesApp(func(context.Context, io.Reader, string, string, string) (*imageclient.UploadResult, error) {
		called = true
		return nil, nil
	})
	// "avatar" is a real image-service preset — the closed set here must
	// still refuse it, or the route is an open proxy into every preset.
	body, ct := multipartBody(t, "avatar", "", true)
	resp, env := postEditImage(t, app, body, ct)
	if resp.StatusCode != http.StatusBadRequest || env.Code == 0 {
		t.Fatalf("status=%d code=%d", resp.StatusCode, env.Code)
	}
	if called {
		t.Fatal("unknown preset must not reach the image service")
	}
}

func TestEditImageUploadRequiresAFile(t *testing.T) {
	app := editImagesApp(func(context.Context, io.Reader, string, string, string) (*imageclient.UploadResult, error) {
		t.Fatal("must not be called")
		return nil, nil
	})
	body, ct := multipartBody(t, "galgame_screenshot", "", false)
	resp, env := postEditImage(t, app, body, ct)
	if resp.StatusCode != http.StatusBadRequest || env.Code == 0 {
		t.Fatalf("status=%d code=%d", resp.StatusCode, env.Code)
	}
}

func TestEditImageUploadWithoutClientIs503(t *testing.T) {
	app := editImagesApp(nil)
	body, ct := multipartBody(t, "galgame_banner", "", true)
	resp, env := postEditImage(t, app, body, ct)
	if resp.StatusCode != http.StatusServiceUnavailable || env.Code == 0 {
		t.Fatalf("status=%d code=%d", resp.StatusCode, env.Code)
	}
}

func TestEditImageUploadMapsQuotaToBadRequest(t *testing.T) {
	app := editImagesApp(func(context.Context, io.Reader, string, string, string) (*imageclient.UploadResult, error) {
		return nil, fmt.Errorf("wrap: %w", imageclient.ErrQuotaExceeded)
	})
	body, ct := multipartBody(t, "galgame_banner", "", true)
	resp, env := postEditImage(t, app, body, ct)
	if resp.StatusCode != http.StatusBadRequest || env.Code == 0 {
		t.Fatalf("status=%d code=%d", resp.StatusCode, env.Code)
	}
}
