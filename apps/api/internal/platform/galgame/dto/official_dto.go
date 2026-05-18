package dto

// ListOfficialRequest represents an official list query
type ListOfficialRequest struct {
	Page  int `query:"page" validate:"min=1"`
	Limit int `query:"limit" validate:"min=1,max=100"`
}

// GetOfficialByNameRequest represents an official detail query
type GetOfficialByNameRequest struct {
	OfficialID   int    `query:"official_id" validate:"required"`
	Page         int    `query:"page" validate:"min=1"`
	Limit        int    `query:"limit" validate:"min=1,max=50"`
	Type         string `query:"type"`
	Language     string `query:"language"`
	Platform     string `query:"platform"`
	ContentLimit string `query:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
	SortField    string `query:"sort_field" validate:"omitempty,oneof=created resource_update_time view"`
	SortOrder    string `query:"sort_order" validate:"omitempty,oneof=asc desc"`
}

// SearchOfficialRequest represents an official search query
type SearchOfficialRequest struct {
	Q string `query:"q" validate:"required,max=200"`
}

// UpdateOfficialRequest represents an official update request
type UpdateOfficialRequest struct {
	OfficialID  int      `json:"official_id" validate:"required"`
	Name        *string  `json:"name"`
	Link        *string  `json:"link"`
	Category    *string  `json:"category" validate:"omitempty,oneof=company individual amateur"`
	Lang        *string  `json:"lang"`
	Description *string  `json:"description"`
	Alias       []string `json:"alias"`
}

// CreateOfficialRequest represents an official (developer/producer)
// creation request. Lets kungal/moyu users introduce a company/circle
// missing from the wiki for an original/doujin work.
type CreateOfficialRequest struct {
	Name        string   `json:"name" validate:"required,max=500"`
	Category    string   `json:"category" validate:"required,oneof=company individual amateur"`
	Original    string   `json:"original" validate:"max=500"`
	Link        string   `json:"link" validate:"max=1000"`
	Lang        string   `json:"lang" validate:"max=20"`
	Description string   `json:"description" validate:"max=2000"`
	Alias       []string `json:"alias"`
}
