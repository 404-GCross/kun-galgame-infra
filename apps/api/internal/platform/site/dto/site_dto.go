package dto

// CreateSiteRequest represents a site creation request
type CreateSiteRequest struct {
	Name        string `json:"name" validate:"required,max=50"`
	Domain      string `json:"domain" validate:"required,max=255"`
	Description string `json:"description"`
}

// UpdateSiteRequest represents a site update request
type UpdateSiteRequest struct {
	Name        *string `json:"name" validate:"omitempty,max=50"`
	Description *string `json:"description"`
}

// SiteResponse represents a site in API responses
type SiteResponse struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// CreateOAuthClientRequest represents an OAuth client creation request.
//
// AllowedScopes is the OAuth scope allow-list (e.g.
// `["openid","profile","email","image:upload"]`). Empty/omitted is
// equivalent to the OIDC core set {openid, profile, email} only —
// sensitive scopes like `image:upload` MUST be explicitly listed.
type CreateOAuthClientRequest struct {
	SiteID        uint     `json:"site_id" validate:"required"`
	Name          string   `json:"name" validate:"required,max=100"`
	RedirectURIs  []string `json:"redirect_uris" validate:"required,min=1"`
	Grants        []string `json:"grants"`
	AllowedScopes []string `json:"allowed_scopes"`
}

// UpdateOAuthClientRequest represents an OAuth client update request.
// AllowedScopes follows the same nil-vs-empty semantic as CreateRequest.
type UpdateOAuthClientRequest struct {
	Name          *string  `json:"name" validate:"omitempty,max=100"`
	RedirectURIs  []string `json:"redirect_uris" validate:"omitempty,min=1"`
	Grants        []string `json:"grants"`
	AllowedScopes []string `json:"allowed_scopes"`
}

// OAuthClientResponse represents an OAuth client in API responses
type OAuthClientResponse struct {
	ID            string   `json:"id"`
	SiteID        *uint    `json:"site_id,omitempty"`
	Name          string   `json:"name"`
	RedirectURIs  []string `json:"redirect_uris"`
	Grants        []string `json:"grants"`
	AllowedScopes []string `json:"allowed_scopes"`
	IsPublic      bool     `json:"is_public"`
	CreatedAt     string   `json:"created_at"`
}

// OAuthClientCreatedResponse includes the secret (only shown once on creation)
type OAuthClientCreatedResponse struct {
	OAuthClientResponse
	Secret string `json:"secret"`
}
