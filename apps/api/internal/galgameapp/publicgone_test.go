package galgameapp

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api/internal/app"
	"api/pkg/errors"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// Wave 146 Lane A delisting contract.
//
// The /v1/galgame public projection is retired; this suite replaces the W1a/W1b
// smoke suites that used to exercise its 26 operations. It pins the REPLACEMENT
// contract instead: every path of the retired census answers 410 with the house
// {code, message} envelope, no credential is involved, and the neighbouring
// prefixes are untouched.
//
// It needs no database and no Meilisearch — the catch-all has no dependencies —
// so it also runs in CI's service-less unit job, where the old smokes skipped.

// retiredPublicPaths is the full census of the delisted face: the 26 operations
// the frozen spec published (list / detail / batch / search / changes / stats /
// lookup / calendar ×3 / entity reverse-lookups ×2 / taxonomy by-id ×14), plus
// the openapi.json the face used to serve un-keyed and two paths that were never
// registered — a retired PREFIX must answer 410 whole, not just on its old
// routes.
var retiredPublicPaths = []string{
	"/v1/galgame",
	"/v1/galgame/1",
	"/v1/galgame/batch?ids=1",
	"/v1/galgame/search?q=x",
	"/v1/galgame/changes",
	"/v1/galgame/stats",
	"/v1/galgame/lookup?vndb_id=v17",
	"/v1/galgame/calendar",
	"/v1/galgame/calendar/pending",
	"/v1/galgame/calendar/tba",
	"/v1/galgame/officials/1/galgames",
	"/v1/galgame/tags/1/galgames",
	"/v1/galgame/tags",
	"/v1/galgame/tags/search?q=x",
	"/v1/galgame/tags/multi?ids=1",
	"/v1/galgame/tags/1",
	"/v1/galgame/tags/1/galgame-ids",
	"/v1/galgame/officials",
	"/v1/galgame/officials/search?q=x",
	"/v1/galgame/officials/1",
	"/v1/galgame/officials/1/galgame-ids",
	"/v1/galgame/engines",
	"/v1/galgame/engines/1",
	"/v1/galgame/engines/1/galgame-ids",
	"/v1/galgame/series",
	"/v1/galgame/series/1",
	// Never-registered paths under the retired prefix.
	"/v1/galgame/openapi.json",
	"/v1/galgame/whatever/deeply/nested",
}

// goneApp builds a bare Fiber app carrying only the retired-face catch-all.
func goneApp() *fiber.App {
	a := &app.App{Fiber: fiber.New()}
	mountRetiredPublic(a)
	return a.Fiber
}

// getGone drives one request and returns status, headers and the decoded
// envelope. apiKey is optional: the whole point is that it changes nothing.
func getGone(t *testing.T, f *fiber.App, method, path, apiKey string) (int, map[string]string, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if apiKey != "" {
		r.Header.Set("X-API-Key", apiKey)
	}
	resp, err := f.Test(r, fiber.TestConfig{Timeout: 5 * time.Second})
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	var env map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &env), "%s %s did not return JSON: %s", method, path, raw)
	hdr := map[string]string{
		"Link":        resp.Header.Get("Link"),
		"Deprecation": resp.Header.Get("Deprecation"),
		"Sunset":      resp.Header.Get("Sunset"),
	}
	return resp.StatusCode, hdr, env
}

func TestRetiredPublicFaceIsGone(t *testing.T) {
	f := goneApp()

	// ── The whole census answers 410 with the house envelope ──
	require.Len(t, retiredPublicPaths, 28, "census = the 26 published ops + openapi.json + one unregistered path")
	for _, path := range retiredPublicPaths {
		status, hdr, env := getGone(t, f, "GET", path, "")
		require.Equalf(t, fiber.StatusGone, status, "GET %s must be 410 Gone", path)
		require.EqualValuesf(t, errors.ErrGone, env["code"], "GET %s must carry the ErrGone code", path)
		msg, _ := env["message"].(string)
		require.Containsf(t, msg, "/v1/catalog", "GET %s must name the successor face", path)
		require.Containsf(t, msg, "2026-07-30", "GET %s must state the retirement date", path)
		require.Containsf(t, msg, "https://developer.nextmoe.dev/docs/catalog", "GET %s must link the docs", path)
		// A 410 body is an error envelope, never a data payload.
		require.NotContainsf(t, env, "data", "GET %s must not carry a data block", path)
		// The successor Link survives; the Deprecation/Sunset pair retired with
		// the face it was announcing (they promise a FUTURE retirement).
		require.Equalf(t, retiredSuccessorLink, hdr["Link"], "GET %s must keep the successor Link", path)
		require.Emptyf(t, hdr["Deprecation"], "GET %s must not still announce a pending deprecation", path)
		require.Emptyf(t, hdr["Sunset"], "GET %s must not still announce a pending sunset", path)
	}
}

func TestRetiredPublicFaceIgnoresCredentialsAndMethods(t *testing.T) {
	f := goneApp()

	// No credential, a junk credential and a well-formed one are the same answer:
	// the face is gone for everyone, and nobody must authenticate to find out.
	for _, key := range []string{"", "not-a-key", "nm_live_0000000000000000"} {
		status, _, env := getGone(t, f, "GET", "/v1/galgame/1", key)
		require.Equal(t, fiber.StatusGone, status, "410 must not depend on the credential")
		require.EqualValues(t, errors.ErrGone, env["code"])
	}

	// Every method, not just GET — a retired prefix must not leak a Fiber 404/405
	// on a verb the old face never registered.
	for _, m := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		r := httptest.NewRequest(m, "/v1/galgame/tags/1", nil)
		resp, err := f.Test(r, fiber.TestConfig{Timeout: 5 * time.Second})
		require.NoError(t, err)
		require.Equalf(t, fiber.StatusGone, resp.StatusCode, "%s must also be 410", m)
	}
}

func TestRetiredPublicCatchAllStaysInsideItsPrefix(t *testing.T) {
	// The catch-all must not swallow the surviving faces. Register stand-ins on
	// the neighbouring prefixes AFTER the catch-all (the harshest ordering) and
	// prove each still reaches its own handler.
	a := &app.App{Fiber: fiber.New()}
	mountRetiredPublic(a)
	ok := func(c fiber.Ctx) error { return c.SendString("survivor") }
	for _, p := range []string{
		"/v1/catalog/works/1",        // canonical public face
		"/v1/catalogue",              // adjacent literal, must not be captured
		"/api/galgame/catalog/stats", // staff face
		"/internal/galgame/mine",     // platform-workflow face
		"/v1/galgamesque",            // shares the retired prefix as a string, not as a path
	} {
		a.Fiber.Get(p, ok)
	}
	f := a.Fiber

	for _, p := range []string{
		"/v1/catalog/works/1", "/v1/catalogue",
		"/api/galgame/catalog/stats", "/internal/galgame/mine", "/v1/galgamesque",
	} {
		r := httptest.NewRequest("GET", p, nil)
		resp, err := f.Test(r, fiber.TestConfig{Timeout: 5 * time.Second})
		require.NoError(t, err)
		raw, _ := io.ReadAll(resp.Body)
		require.Equalf(t, fiber.StatusOK, resp.StatusCode, "surviving route %s must not be captured by the 410 catch-all", p)
		require.Equalf(t, "survivor", strings.TrimSpace(string(raw)), "surviving route %s must reach its own handler", p)
	}
}
