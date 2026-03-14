package dto

// CreateGameRequest represents a game creation request
type CreateGameRequest struct {
	Name        string   `json:"name" validate:"required,max=255"`
	Description string   `json:"description"`
	Developer   string   `json:"developer"`
	Publisher   string   `json:"publisher"`
	TagIDs      []uint   `json:"tag_ids"`
}

// UpdateGameRequest represents a game update request
type UpdateGameRequest struct {
	Name        *string  `json:"name" validate:"omitempty,max=255"`
	Description *string  `json:"description"`
	Developer   *string  `json:"developer"`
	Publisher   *string  `json:"publisher"`
	TagIDs      []uint   `json:"tag_ids"`
}

// GameResponse represents a game in API responses
type GameResponse struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CoverImage  string   `json:"cover_image"`
	Developer   string   `json:"developer"`
	Publisher   string   `json:"publisher"`
	Status      int      `json:"status"`
	Tags        []string `json:"tags"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}
