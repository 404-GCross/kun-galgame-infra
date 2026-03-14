package dto

// CreateContentRequest represents a content creation request
type CreateContentRequest struct {
	SiteID uint   `json:"site_id" validate:"required"`
	Title  string `json:"title" validate:"required,max=255"`
	Body   string `json:"body" validate:"required"`
}

// UpdateContentRequest represents a content update request
type UpdateContentRequest struct {
	Title  *string `json:"title" validate:"omitempty,max=255"`
	Body   *string `json:"body"`
	Status *int    `json:"status"`
}

// ContentResponse represents content in API responses
type ContentResponse struct {
	UUID             string `json:"uuid"`
	SiteID           uint   `json:"site_id"`
	UserUUID         string `json:"user_uuid"`
	Title            string `json:"title"`
	Body             string `json:"body"`
	Status           int    `json:"status"`
	ModerationStatus int    `json:"moderation_status"`
	ViewCount        int    `json:"view_count"`
	LikeCount        int    `json:"like_count"`
	CommentCount     int    `json:"comment_count"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}
