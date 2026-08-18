package main

import (
	"net/http/httptest"
	"testing"

	"api/internal/platform/devapi"

	"github.com/gofiber/fiber/v3"
)

// setupPublicCatalog needs Postgres, Redis and Meilisearch, so this pins the
// registration ORDER it depends on rather than calling it: /v1/catalog/stats is
// mounted bare on the app and must sit above the /v1/catalog group, whose
// handlers are a prefix Use. The reverse-order case is the control — without it
// a green test would only prove the gate exists, not that the ordering carries
// the anonymous access.
func statsRouteApp(barefirst bool) *fiber.App {
	f := fiber.New()
	stats := func(c fiber.Ctx) error { return c.SendString("stats") }
	mount := func() {
		g := f.Group("/v1/catalog", devapi.RequireScope(devapi.ScopeCatalogRead))
		g.Get("/works", func(c fiber.Ctx) error { return c.SendString("works") })
	}
	if barefirst {
		f.Get("/v1/catalog/stats", stats)
		mount()
	} else {
		mount()
		f.Get("/v1/catalog/stats", stats)
	}
	return f
}

func statusOf(t *testing.T, f *fiber.App, path string) int {
	t.Helper()
	resp, err := f.Test(httptest.NewRequest("GET", path, nil))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode
}

func TestPublicStatsIsReachableWithoutACredential(t *testing.T) {
	f := statsRouteApp(true)

	if got := statusOf(t, f, "/v1/catalog/stats"); got != fiber.StatusOK {
		t.Errorf("anonymous GET /v1/catalog/stats = %d, want 200", got)
	}
	if got := statusOf(t, f, "/v1/catalog/works"); got != fiber.StatusUnauthorized {
		t.Errorf("anonymous GET /v1/catalog/works = %d, want 401 — the key gate must still cover the rest of the face", got)
	}
}

func TestPublicStatsMountedBelowTheGroupStaysGated(t *testing.T) {
	f := statsRouteApp(false)

	if got := statusOf(t, f, "/v1/catalog/stats"); got != fiber.StatusUnauthorized {
		t.Errorf("stats registered after the group = %d, want 401", got)
	}
}
