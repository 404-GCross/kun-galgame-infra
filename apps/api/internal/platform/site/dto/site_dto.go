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

// CreateOAuthClientRequest represents an OAuth client creation request
type CreateOAuthClientRequest struct {
	SiteID       uint     `json:"site_id" validate:"required"`
	Name         string   `json:"name" validate:"required,max=100"`
	RedirectURIs []string `json:"redirect_uris" validate:"required,min=1"`
	Grants       []string `json:"grants"`
}

// UpdateOAuthClientRequest represents an OAuth client update request
type UpdateOAuthClientRequest struct {
	Name         *string  `json:"name" validate:"omitempty,max=100"`
	RedirectURIs []string `json:"redirect_uris" validate:"omitempty,min=1"`
	Grants       []string `json:"grants"`
}

// OAuthClientResponse represents an OAuth client in API responses
type OAuthClientResponse struct {
	ID           string   `json:"id"`
	SiteID       *uint    `json:"site_id,omitempty"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	Grants       []string `json:"grants"`
	CreatedAt    string   `json:"created_at"`
}

// OAuthClientCreatedResponse includes the secret (only shown once on creation)
type OAuthClientCreatedResponse struct {
	OAuthClientResponse
	Secret string `json:"secret"`
}
