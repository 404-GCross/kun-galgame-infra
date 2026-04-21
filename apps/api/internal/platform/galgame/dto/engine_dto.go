package dto

// GetEngineByNameRequest represents an engine detail query
type GetEngineByNameRequest struct {
	EngineID     int    `query:"engine_id" validate:"required"`
	Page         int    `query:"page" validate:"min=1"`
	Limit        int    `query:"limit" validate:"min=1,max=50"`
	ContentLimit string `query:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
}

// UpdateEngineRequest represents an engine update request
type UpdateEngineRequest struct {
	EngineID    int      `json:"engine_id" validate:"required"`
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Alias       []string `json:"alias"`
}
