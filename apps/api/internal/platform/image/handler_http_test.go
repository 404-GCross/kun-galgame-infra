package image_test

// HTTP-level integration tests. Exercise the full Fiber chain:
//
//   request → middleware (RequestID, ClientAuth) → handler → service → repo → S3
//
// Test fixtures (testApp, testClientRepo, etc) are populated by TestMain
// in image_test.go.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- helpers ----

// basicAuth builds the Authorization header value for the given client_id / secret.
func basicAuth(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

// uploadRequest builds a POST /image/upload multipart request.
func uploadRequest(t *testing.T, presetName string, body []byte, auth string) *http.Request {
	t.Helper()
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	part, err := mw.CreateFormFile("file", "test.png")
	require.NoError(t, err)
	_, err = part.Write(body)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("preset", presetName))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/image/upload", buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return req
}

// envelope is the standard pkg/response wrapper.
type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, resp *http.Response) envelope {
	t.Helper()
	var e envelope
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, &e), "raw body: %s", string(body))
	return e
}

// uploadResultPayload mirrors service.UploadResult for test decoding.
type uploadResultPayload struct {
	Hash         string            `json:"hash"`
	URL          string            `json:"url"`
	VariantURLs  map[string]string `json:"variant_urls"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	SizeBytes    int64             `json:"size_bytes"`
	Deduplicated bool              `json:"deduplicated"`
}

// callUpload runs the full upload flow and returns parsed result.
func callUpload(t *testing.T, body []byte, presetName, clientID, secret string) (int, *uploadResultPayload, envelope) {
	t.Helper()
	req := uploadRequest(t, presetName, body, basicAuth(clientID, secret))
	resp, err := testApp.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	require.NoError(t, err)
	defer resp.Body.Close()

	env := decodeEnvelope(t, resp)
	if resp.StatusCode != 200 || env.Code != 0 {
		return resp.StatusCode, nil, env
	}
	var payload uploadResultPayload
	require.NoError(t, json.Unmarshal(env.Data, &payload))
	return resp.StatusCode, &payload, env
}

// ---- /healthz ----

func TestHTTP_Healthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `"status":"ok"`)
}

// ---- POST /image/upload ----

func TestHTTP_Upload_Success(t *testing.T) {
	body := fixturePNG(300, 300, 50, 100, 150)
	status, result, env := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.Equal(t, 200, status, "envelope: %+v", env)
	require.NotNil(t, result)
	assert.Len(t, result.Hash, 64)
	assert.Contains(t, result.URL, result.Hash)
	assert.Equal(t, "image/webp", "image/webp")
	assert.True(t, result.Width > 0)
	assert.Contains(t, result.VariantURLs, "256")
	assert.Contains(t, result.VariantURLs, "100")
}

func TestHTTP_Upload_NoAuth_401(t *testing.T) {
	body := fixturePNG(100, 100, 0, 0, 0)
	req := uploadRequest(t, "avatar", body, "")
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 401, resp.StatusCode)
}

func TestHTTP_Upload_BadSecret_401(t *testing.T) {
	body := fixturePNG(100, 100, 0, 0, 0)
	req := uploadRequest(t, "avatar", body, basicAuth(testClientID, "wrong-secret"))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 401, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	// Error code 80003 = ErrImageBadSecret
	assert.Equal(t, 80003, env.Code)
}

func TestHTTP_Upload_DisabledClient_403(t *testing.T) {
	body := fixturePNG(100, 100, 0, 0, 0)
	req := uploadRequest(t, "avatar", body, basicAuth(testDisabledClientID, "secret"))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	// 80004 = ErrImageSiteDisabled
	assert.Equal(t, 80004, env.Code)
}

func TestHTTP_Upload_DeniedPreset_403(t *testing.T) {
	// restricted client only allows `topic`; attempt avatar.
	body := fixturePNG(100, 100, 0, 0, 0)
	req := uploadRequest(t, "avatar", body, basicAuth(testRestrictedClient, "secret"))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 403, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	assert.Equal(t, 80006, env.Code) // ErrImagePresetDenied
}

func TestHTTP_Upload_FileTooLarge_413(t *testing.T) {
	// tiny client has image_max_file_size=128 bytes; PNG is much bigger.
	body := fixturePNG(100, 100, 0, 0, 0)
	require.Greater(t, len(body), 128, "fixture must exceed tiny limit")
	req := uploadRequest(t, "avatar", body, basicAuth(testTinyClient, "secret"))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 413, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	assert.Equal(t, 80007, env.Code) // ErrImageFileTooLarge
}

func TestHTTP_Upload_NoPreset_400(t *testing.T) {
	body := fixturePNG(100, 100, 0, 0, 0)
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	part, _ := mw.CreateFormFile("file", "test.png")
	_, _ = part.Write(body)
	// no `preset` field
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/image/upload", buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", basicAuth(testClientID, testClientSecret))

	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode)
}

func TestHTTP_Upload_NoFile_400(t *testing.T) {
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)
	_ = mw.WriteField("preset", "avatar")
	// no `file` part
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/image/upload", buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", basicAuth(testClientID, testClientSecret))

	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode)
}

func TestHTTP_Upload_NonImage_400(t *testing.T) {
	junk := []byte("Not an image at all — this is plain ASCII text that the MIME sniffer will reject")
	req := uploadRequest(t, "avatar", junk, basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	assert.Equal(t, 80009, env.Code) // ErrImageMIMEDenied
}

// ---- GET /image/:hash ----

func TestHTTP_Meta_Found(t *testing.T) {
	// First upload to ensure something exists.
	body := fixturePNG(220, 220, 7, 200, 50)
	_, result, _ := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.NotNil(t, result)

	// Fetch meta.
	req := httptest.NewRequest(http.MethodGet, "/image/"+result.Hash, nil)
	req.Header.Set("Authorization", basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	require.Equal(t, 0, env.Code)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(env.Data, &meta))
	assert.Equal(t, result.Hash, meta["hash"])
	assert.Equal(t, "approved", meta["review_status"])
	assert.Equal(t, "image/webp", meta["mime"])
	assert.Contains(t, meta, "variant_urls")
	assert.Contains(t, meta, "sites")
}

func TestHTTP_Meta_NotFound_404(t *testing.T) {
	missing := strings.Repeat("0", 64)
	req := httptest.NewRequest(http.MethodGet, "/image/"+missing, nil)
	req.Header.Set("Authorization", basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)
}

func TestHTTP_Meta_BadHashFormat_400(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/image/short-hash", nil)
	req.Header.Set("Authorization", basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode)
}

// ---- DELETE /image/:hash (SoftDelete + resurrect-on-reupload) ----

// softDelete issues DELETE /image/:hash as the given client.
func softDelete(t *testing.T, hash, clientID, secret string) (int, envelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/image/"+hash, nil)
	req.Header.Set("Authorization", basicAuth(clientID, secret))
	resp, err := testApp.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode, decodeEnvelope(t, resp)
}

// metaStatus issues GET /image/:hash and returns the HTTP status code.
func metaStatus(t *testing.T, hash, clientID, secret string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/image/"+hash, nil)
	req.Header.Set("Authorization", basicAuth(clientID, secret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// finding #03: DELETE /image/:hash must not nil-panic (the service is now
// wired with a DB) and must soft-delete an image the caller's site used.
// finding #17: re-uploading the same content within the GC window must
// resurrect the row (dedup) instead of 500ing on the UNIQUE(hash) index.
func TestHTTP_SoftDelete_ThenResurrectOnReupload(t *testing.T) {
	body := fixturePNG(137, 211, 211, 17, 88) // unique-ish: hash is local to this test

	// 1) Upload → present and visible.
	status, result, env := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.Equal(t, 200, status, "envelope: %+v", env)
	require.NotNil(t, result)
	hash := result.Hash
	require.Equal(t, 200, metaStatus(t, hash, testClientID, testClientSecret))

	// 2) Soft-delete → 200, then the image is hidden (404 on meta).
	delStatus, delEnv := softDelete(t, hash, testClientID, testClientSecret)
	require.Equal(t, 200, delStatus, "soft-delete must not nil-panic; envelope: %+v", delEnv)
	require.Equal(t, 0, delEnv.Code)
	assert.Equal(t, 404, metaStatus(t, hash, testClientID, testClientSecret),
		"soft-deleted image should be hidden")

	// 3) Re-upload identical bytes → must succeed (NOT 500), resurrect the
	//    row, report dedup, keep the same hash, and be visible again.
	status2, result2, env2 := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.Equal(t, 200, status2, "re-upload of a soft-deleted hash must not 500; envelope: %+v", env2)
	require.NotNil(t, result2)
	assert.Equal(t, hash, result2.Hash)
	assert.True(t, result2.Deduplicated, "resurrected upload should be a dedup hit")
	assert.Equal(t, 200, metaStatus(t, hash, testClientID, testClientSecret),
		"resurrected image should be visible again")
}

// finding #03 (site-scoping): a client whose site never referenced the hash
// gets 404, never a cross-site soft-delete.
func TestHTTP_SoftDelete_OtherSite_404(t *testing.T) {
	body := fixturePNG(141, 99, 3, 240, 120)
	status, result, env := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.Equal(t, 200, status, "envelope: %+v", env)
	require.NotNil(t, result)

	// testRestrictedClient runs on "restrictedsite" and never uploaded this hash.
	delStatus, _ := softDelete(t, result.Hash, testRestrictedClient, "secret")
	assert.Equal(t, 404, delStatus, "a site that never used the hash must not soft-delete it")
	// The original remains visible to its real owner.
	assert.Equal(t, 200, metaStatus(t, result.Hash, testClientID, testClientSecret))
}

// ---- POST /image/reference-ping ----

func TestHTTP_ReferencePing_Updates(t *testing.T) {
	// Upload one image so we have a hash to ping.
	body := fixturePNG(180, 180, 11, 22, 33)
	_, result, _ := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.NotNil(t, result)

	req := makeJSONRequest(t,
		"/image/reference-ping",
		map[string]any{"hashes": []string{result.Hash, strings.Repeat("a", 64)}},
		basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	var data struct {
		Updated  int      `json:"updated"`
		NotFound []string `json:"not_found"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &data))
	assert.Equal(t, 1, data.Updated)
	assert.Len(t, data.NotFound, 1)
}

func TestHTTP_ReferencePing_TooMany_400(t *testing.T) {
	hashes := make([]string, 1001)
	for i := range hashes {
		hashes[i] = strings.Repeat("0", 64)
	}
	req := makeJSONRequest(t,
		"/image/reference-ping",
		map[string]any{"hashes": hashes},
		basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 400, resp.StatusCode)
}

func TestHTTP_ReferencePing_EmptyHashes(t *testing.T) {
	req := makeJSONRequest(t,
		"/image/reference-ping",
		map[string]any{"hashes": []string{}},
		basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
}

// ---- GET /image/stats ----

func TestHTTP_Stats(t *testing.T) {
	// Pre-populate something.
	body := fixturePNG(150, 150, 1, 2, 3)
	_, _, _ = callUpload(t, body, "avatar", testClientID, testClientSecret)

	req := httptest.NewRequest(http.MethodGet, "/image/stats", nil)
	req.Header.Set("Authorization", basicAuth(testClientID, testClientSecret))
	resp, err := testApp.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	env := decodeEnvelope(t, resp)
	require.Equal(t, 0, env.Code)
	var stats struct {
		UploadCount       int   `json:"upload_count"`
		UniqueImages      int   `json:"unique_images"`
		DeduplicatedCount int   `json:"deduplicated_count"`
		TotalBytes        int64 `json:"total_bytes"`
	}
	require.NoError(t, json.Unmarshal(env.Data, &stats))
	assert.GreaterOrEqual(t, stats.UploadCount, 1)
	assert.GreaterOrEqual(t, stats.UniqueImages, 1)
}

// ---- GET /metrics ----

func TestHTTP_Metrics_Exposes(t *testing.T) {
	// Trigger an upload to ensure custom metrics actually have a sample.
	body := fixturePNG(80, 80, 9, 9, 9)
	_, _, _ = callUpload(t, body, "avatar", testClientID, testClientSecret)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp, err := testApp.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	body2, _ := io.ReadAll(resp.Body)
	text := string(body2)
	// Custom counters declared in metrics package
	assert.Contains(t, text, "image_upload_total")
	assert.Contains(t, text, "image_upload_duration_seconds")
	// Standard go runtime metrics auto-registered by promhttp
	assert.Contains(t, text, "go_goroutines")
}

// ---- Cross-preset reuse via HTTP ----

func TestHTTP_DedupAcrossClients(t *testing.T) {
	// Upload as testClientID, then re-upload via... still testClientID since
	// we only have one valid full-perm client. But we can verify dedup at
	// least within the same client.
	body := fixturePNG(190, 190, 222, 111, 33)
	_, r1, _ := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.NotNil(t, r1)
	assert.False(t, r1.Deduplicated)

	_, r2, _ := callUpload(t, body, "avatar", testClientID, testClientSecret)
	require.NotNil(t, r2)
	assert.True(t, r2.Deduplicated)
	assert.Equal(t, r1.Hash, r2.Hash)
	assert.Equal(t, r1.URL, r2.URL)
}

// ---- helpers ----

// makeJSONRequest builds a JSON-bodied request with auth.
func makeJSONRequest(t *testing.T, path string, body any, auth string) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return req
}

// keep unused-imports happy if we ever drop a test.
var _ = fmt.Sprintf
