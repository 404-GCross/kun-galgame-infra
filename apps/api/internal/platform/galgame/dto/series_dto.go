package dto

// ListSeriesRequest represents a series list query
type ListSeriesRequest struct {
	Page  int `query:"page" validate:"min=1"`
	Limit int `query:"limit" validate:"min=1,max=50"`
}

// SearchSeriesRequest represents a series search query (searches galgames)
type SearchSeriesRequest struct {
	Keywords string `query:"keywords" validate:"required,max=200"`
}

// CreateSeriesRequest represents a series creation request
type CreateSeriesRequest struct {
	Name        string `json:"name" validate:"required,max=1000"`
	Description string `json:"description" validate:"max=2000"`
	GalgameIDs  []int  `json:"galgame_ids"`
}

// UpdateSeriesRequest represents a series update request
type UpdateSeriesRequest struct {
	Name        *string `json:"name" validate:"omitempty,max=1000"`
	Description *string `json:"description" validate:"omitempty,max=2000"`
	GalgameIDs  []int   `json:"galgame_ids"`
}

// ModalRequest represents a request to get galgames by IDs
type ModalRequest struct {
	IDs []int `json:"ids" validate:"required"`
}
