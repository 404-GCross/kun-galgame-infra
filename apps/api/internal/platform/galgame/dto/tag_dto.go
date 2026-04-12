package dto

// ListTagRequest represents a tag list query
type ListTagRequest struct {
	Page  int `query:"page" validate:"min=1"`
	Limit int `query:"limit" validate:"min=1,max=100"`
}

// GetTagByNameRequest represents a tag detail query
type GetTagByNameRequest struct {
	TagID     int    `query:"tagId" validate:"required"`
	Page      int    `query:"page" validate:"min=1"`
	Limit     int    `query:"limit" validate:"min=1,max=50"`
	Type      string `query:"type"`
	Language  string `query:"language"`
	Platform  string `query:"platform"`
	SortField string `query:"sortField" validate:"omitempty,oneof=created resource_update_time view"`
	SortOrder string `query:"sortOrder" validate:"omitempty,oneof=asc desc"`
}

// SearchTagRequest represents a tag search query
type SearchTagRequest struct {
	Q string `query:"q" validate:"required,max=200"`
}

// MultiTagRequest represents a multi-tag filter query
type MultiTagRequest struct {
	Page   int    `query:"page" validate:"min=1"`
	Limit  int    `query:"limit" validate:"min=1,max=50"`
	TagIDs []int  `query:"tagIds"`
}

// UpdateTagRequest represents a tag update request
type UpdateTagRequest struct {
	TagID       int      `json:"tag_id" validate:"required"`
	Name        *string  `json:"name"`
	Category    *string  `json:"category" validate:"omitempty,oneof=content sexual technical"`
	Description *string  `json:"description"`
	Alias       []string `json:"alias"`
}
