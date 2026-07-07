package handler

import (
	"api/pkg/catalogclient"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// CatalogProxyHandler relays the internal data browser's read-only requests to
// the catalog S2S read face, keeping the Basic credentials server-side (the
// wiki frontend never sees them). The routes are staff-gated (admin/moderator)
// by the caller; this handler adds no logic beyond forwarding.
type CatalogProxyHandler struct{ cli *catalogclient.Client }

// NewCatalogProxyHandler wraps a catalog client (may be nil when unconfigured →
// every route soft-503s).
func NewCatalogProxyHandler(cli *catalogclient.Client) *CatalogProxyHandler {
	return &CatalogProxyHandler{cli: cli}
}

func (h *CatalogProxyHandler) forward(c fiber.Ctx, catalogPath string) error {
	if h.cli == nil {
		return response.Error(c, fiber.StatusServiceUnavailable, errors.ErrInternalServer,
			"catalog browsing not configured (KUN_CATALOG_CLIENT_ID/SECRET unset)")
	}
	status, body, err := h.cli.GetJSON(c.Context(), catalogPath, string(c.Request().URI().QueryString()))
	if err != nil {
		return response.Error(c, fiber.StatusBadGateway, errors.ErrInternalServer, "catalog service unreachable")
	}
	c.Set("Content-Type", "application/json")
	return c.Status(status).Send(body)
}

// Stats → GET /catalog/stats
func (h *CatalogProxyHandler) Stats(c fiber.Ctx) error { return h.forward(c, "stats") }

// Search → GET /catalog/search/entities (query forwarded)
func (h *CatalogProxyHandler) Search(c fiber.Ctx) error { return h.forward(c, "search/entities") }

// Work → GET /catalog/works/{id}
func (h *CatalogProxyHandler) Work(c fiber.Ctx) error {
	return h.forward(c, "works/"+c.Params("id"))
}

// Credits → GET /catalog/works/{id}/credits
func (h *CatalogProxyHandler) Credits(c fiber.Ctx) error {
	return h.forward(c, "works/"+c.Params("id")+"/credits")
}

// LabelWorks → GET /catalog/labels/{id}/works (query forwarded for pagination)
func (h *CatalogProxyHandler) LabelWorks(c fiber.Ctx) error {
	return h.forward(c, "labels/"+c.Params("id")+"/works")
}
