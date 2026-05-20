package dto

import "time"

// MessageGalgameBrief is the inline galgame summary attached to every
// message. Reflects the galgame's CURRENT state (not at message time).
// May be nil if the galgame was deleted (withdrawn submission).
type MessageGalgameBrief struct {
	ID                  int     `json:"id"`
	NameEnUS            string  `json:"name_en_us"`
	NameJaJP            string  `json:"name_ja_jp"`
	NameZhCN            string  `json:"name_zh_cn"`
	NameZhTW            string  `json:"name_zh_tw"`
	Banner              string  `json:"banner"`
	// EffectiveBannerHash is the pinned cover (sort_order=0) hash — the
	// only image-service banner reference now that PR5 retired
	// banner_image_hash. Frontends render thumbnails via resolveBannerUrl.
	EffectiveBannerHash *string `json:"effective_banner_hash,omitempty"`
	Status              int     `json:"status"`
	UserID              int     `json:"user_id"`
}

// MessageResponse is the wire format for one galgame_message row.
type MessageResponse struct {
	ID            int64                `json:"id"`
	Type          string               `json:"type"`
	GalgameID     int                  `json:"galgame_id"`
	Galgame       *MessageGalgameBrief `json:"galgame,omitempty"`
	ActorUserID   *int                 `json:"actor_user_id,omitempty"`
	Actor         *UserBrief           `json:"actor,omitempty"` // populated for admin queue only
	TargetUserID  *int                 `json:"target_user_id,omitempty"`
	Payload       any                  `json:"payload,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
}

// ListMineMessagesRequest is the query of GET /galgame/messages/mine.
type ListMineMessagesRequest struct {
	SinceID int64 `query:"since_id" validate:"omitempty,min=0"`
	Limit   int   `query:"limit" validate:"omitempty,min=1,max=100"`
}

// FeedMessagesRequest is the query of GET /galgame/messages/feed.
type FeedMessagesRequest struct {
	SinceID int64 `query:"since_id" validate:"omitempty,min=0"`
	Limit   int   `query:"limit" validate:"omitempty,min=1,max=5000"`
}

// MessageFeedResponse wraps the feed result with has_more flag for cursors.
type MessageFeedResponse struct {
	Items   []MessageResponse `json:"items"`
	HasMore bool              `json:"has_more"`
}

// AdminQueueRequest is the query of GET /admin/galgame/messages.
type AdminQueueRequest struct {
	// CSV of types to include. Default "submitted,edited_pending".
	Type  string `query:"type"`
	Page  int    `query:"page" validate:"omitempty,min=1"`
	Limit int    `query:"limit" validate:"omitempty,min=1,max=100"`
}

// AdminStatusRequest extends AdminUpdateGalgameStatusRequest with reason.
// Kept compatible by adding Reason optionally.
// (We're extending the existing AdminUpdateGalgameStatusRequest in galgame_dto.go.)
