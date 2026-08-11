package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// mount reproduces cmd/catalog's registration, including the order: /sources is
// a literal segment competing with /:id, and registering it second would make
// GET /v1/news/sources parse "sources" as an id and 404.
func mount(h *PublicHandler) *fiber.App {
	app := fiber.New()
	g := app.Group("/v1/news")
	g.Get("/sources", h.Sources)
	g.Get("/", h.List)
	g.Get("/:id", h.Detail)
	return app
}

func do(t *testing.T, app *fiber.App, path string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestDegradedFaceIsServiceUnavailable: an unreachable kun_news must not take
// catalog down with it, and it must not look like an empty feed either — a
// consumer that cannot tell "no news" from "no database" will cache the void.
func TestDegradedFaceIsServiceUnavailable(t *testing.T) {
	app := mount(NewPublicHandler(nil))
	for _, path := range []string{"/v1/news", "/v1/news/sources", "/v1/news/12"} {
		status, body := do(t, app, path)
		if status != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503 (body %s)", path, status, body)
		}
	}
}

func TestSourcesIsNotParsedAsAnID(t *testing.T) {
	app := mount(NewPublicHandler(nil))
	_, body := do(t, app, "/v1/news/sources")
	if strings.Contains(body, "not found") {
		t.Errorf("/v1/news/sources was routed to the detail handler: %s", body)
	}
}

func TestBadParamsAreRejected(t *testing.T) {
	if _, ok := parseLimit("51"); ok {
		t.Error("limit 51 must be rejected")
	}
	if _, ok := parseLimit("0"); ok {
		t.Error("limit 0 must be rejected")
	}
	if n, ok := parseLimit(""); !ok || n != 20 {
		t.Errorf("default limit = %d,%v, want 20,true", n, ok)
	}
	if _, ok := parseTime("2026-08-10"); ok {
		t.Error("a bare date must be rejected; the contract says RFC3339")
	}
	if _, ok := parseTime("2026-08-10T00:00:00Z"); !ok {
		t.Error("RFC3339 must be accepted")
	}
	if _, ok := parseWorkID("-1"); ok {
		t.Error("negative work_id must be rejected")
	}
	if got := parseCSV(" ymgal , galgame_hihyou "); len(got) != 2 || got[0] != "ymgal" {
		t.Errorf("parseCSV = %v", got)
	}
	if got := parseCSV("all"); got != nil {
		t.Errorf("parseCSV(all) = %v, want nil (no filter)", got)
	}
}
