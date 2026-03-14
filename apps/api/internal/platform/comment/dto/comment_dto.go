package dto

// CreateCommentRequest represents a comment creation request
type CreateCommentRequest struct {
	ContentUUID string `json:"content_uuid" validate:"required"`
	ParentID    *uint  `json:"parent_id"`
	Body        string `json:"body" validate:"required,max=10000"`
}

// UpdateCommentStatusRequest represents a status update request
type UpdateCommentStatusRequest struct {
	Status int `json:"status" validate:"required,oneof=0 1"`
}

// CommentResponse represents a comment in API responses
type CommentResponse struct {
	UUID             string `json:"uuid"`
	ContentUUID      string `json:"content_uuid"`
	UserUUID         string `json:"user_uuid"`
	ParentID         *uint  `json:"parent_id,omitempty"`
	Body             string `json:"body"`
	Status           int    `json:"status"`
	ModerationStatus int    `json:"moderation_status"`
	LikeCount        int    `json:"like_count"`
	ReplyCount       int    `json:"reply_count"`
	CreatedAt        string `json:"created_at"`
}
