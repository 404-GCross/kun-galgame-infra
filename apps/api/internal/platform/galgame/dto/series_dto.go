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

// UpdateSeriesRequest represents a series update request.
//
// SeriesID is bound from the URL path (`/series/:id`) by the handler,
// not from the body — kept as a struct field so the service signature
// is uniform with UpdateTagRequest / UpdateOfficialRequest.
//
// GalgameIDs uses *[]int presence (nil omitted = leave membership
// alone; non-nil incl. empty = authoritative full membership). Per
// §7.5 the membership change produces galgame_revision rows
// (changed_fields=['series_id']) on each affected galgame, NOT a
// series-side revision — series_revision only audits Name/Description.
type UpdateSeriesRequest struct {
	SeriesID    int     `json:"-"`
	Name        *string `json:"name" validate:"omitempty,max=1000"`
	Description *string `json:"description" validate:"omitempty,max=2000"`
	GalgameIDs  *[]int  `json:"galgame_ids"`
}

// RevertSeriesRequest represents the body of POST /series/:id/revert.
type RevertSeriesRequest struct {
	Revision int `json:"revision" validate:"required,min=1"`
}

// ModalRequest represents a request to get galgames by IDs
type ModalRequest struct {
	IDs []int `json:"ids" validate:"required"`
}
