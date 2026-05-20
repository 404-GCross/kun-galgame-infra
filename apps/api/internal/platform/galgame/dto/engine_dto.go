package dto

// GetEngineByNameRequest represents an engine detail query
type GetEngineByNameRequest struct {
	EngineID     int    `query:"engine_id" validate:"required"`
	Page         int    `query:"page" validate:"min=1"`
	Limit        int    `query:"limit" validate:"min=1,max=50"`
	ContentLimit string `query:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
}

// UpdateEngineRequest represents an engine update request. Pointer-
// presence convention same as UpdateTagRequest (§1.5 #5).
type UpdateEngineRequest struct {
	EngineID    int       `json:"engine_id" validate:"required"`
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Alias       *[]string `json:"alias"`
}

// RevertEngineRequest represents the body of POST /engine/:id/revert.
type RevertEngineRequest struct {
	Revision int `json:"revision" validate:"required,min=1"`
}

// CreateEngineRequest represents an engine creation request. Lets
// kungal/moyu users introduce an engine missing from the wiki.
type CreateEngineRequest struct {
	Name        string   `json:"name" validate:"required,max=500"`
	Description string   `json:"description" validate:"max=2000"`
	Alias       []string `json:"alias"`
}
