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
