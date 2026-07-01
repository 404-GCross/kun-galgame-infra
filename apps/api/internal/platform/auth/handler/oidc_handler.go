package handler

import (
	"strings"

	"api/internal/platform/auth/service"
	"api/pkg/config"

	"github.com/gofiber/fiber/v3"
)

// OIDCHandler serves the public OIDC/OAuth metadata endpoints: discovery
// (.well-known) and JWKS. These are world-readable (ACAO *) and return the
// standard top-level JSON shape — NOT the house {code,message,data} envelope.
//
// Design: docs/auth/03-oidc-standardization-design.md §5.4.
type OIDCHandler struct {
	svc *service.SigningKeyService
	cfg *config.Config
}

// NewOIDCHandler creates a new OIDCHandler.
func NewOIDCHandler(svc *service.SigningKeyService, cfg *config.Config) *OIDCHandler {
	return &OIDCHandler{svc: svc, cfg: cfg}
}

// JWKS serves GET /oauth/jwks — the JWK Set of all published public keys.
func (h *OIDCHandler) JWKS(c fiber.Ctx) error {
	set, err := h.svc.JWKS(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "server_error"})
	}
	h.publicHeaders(c)
	return c.JSON(set)
}

// Discovery serves the OIDC discovery + RFC 8414 metadata (identical body).
func (h *OIDCHandler) Discovery(c fiber.Ctx) error {
	h.publicHeaders(c)
	return c.JSON(h.metadata())
}

// publicHeaders marks these documents world-readable + cacheable. They carry no
// secrets and no credentials, so ACAO * is correct (browser-based OIDC clients
// fetch discovery/JWKS cross-origin).
func (h *OIDCHandler) publicHeaders(c fiber.Ctx) {
	c.Set("Access-Control-Allow-Origin", "*")
	c.Set("Cache-Control", "public, max-age=300")
}

// metadata builds the discovery document from the configured issuer. Endpoints
// live under /api/v1/oauth; jwks_uri is the root /oauth/jwks (served before the
// origin-restricted CORS middleware so it stays world-readable).
func (h *OIDCHandler) metadata() fiber.Map {
	iss := strings.TrimRight(h.cfg.Server.SiteURL, "/")
	api := iss + "/api/v1/oauth"
	return fiber.Map{
		"issuer":                                iss,
		"authorization_endpoint":                api + "/authorize",
		"token_endpoint":                        api + "/token",
		"userinfo_endpoint":                     api + "/userinfo",
		"revocation_endpoint":                   api + "/revoke",
		"end_session_endpoint":                  api + "/logout",
		"jwks_uri":                              iss + "/oauth/jwks",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"ES256", "RS256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"claims_supported": []string{
			"iss", "sub", "aud", "exp", "iat", "nonce",
			"name", "email", "picture", "roles",
		},
	}
}
