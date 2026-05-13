package model

import (
	"time"

	"gorm.io/datatypes"
)

// Message type constants — values stored in galgame_message.type column.
// Keep in sync with docs/galgame_wiki/06-submission-and-review-design.md §4.4.
const (
	MessageTypeSubmitted     = "submitted"
	MessageTypeClaimed       = "claimed"
	MessageTypeEditedPending = "edited_pending"
	MessageTypeApproved      = "approved"
	MessageTypeDeclined      = "declined"
	MessageTypeBanned        = "banned"
)

// Galgame status constants for the extended state machine.
// See docs/galgame_wiki/06-submission-and-review-design.md §3.
const (
	GalgameStatusPublished     = 0
	GalgameStatusBanned        = 1
	GalgameStatusVNDBDraft     = 2
	GalgameStatusPending       = 3
	GalgameStatusDeclined      = 4
)

// GalgameMessage records submission / review events in the wiki.
// Consumed by:
//   - Admin web UI (queue view, type='submitted'|'edited_pending')
//   - End users via /messages/mine (their own approved/declined notifications)
//   - kungal/moyu cron via /messages/feed (sync local wiki_status_snapshot)
//
// Read state is NOT tracked here — each consumer keeps its own
// last_read_message_id locally. See 08-messages.md.
type GalgameMessage struct {
	ID            int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Type          string         `gorm:"size:20;not null;index" json:"type"`
	GalgameID     int            `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	ActorUserID   *int           `gorm:"column:actor_user_id" json:"actor_user_id"`
	TargetUserID  *int           `gorm:"column:target_user_id;index" json:"target_user_id"`
	Payload       datatypes.JSON `gorm:"type:jsonb" json:"payload,omitempty"`
	CreatedAt     time.Time      `gorm:"autoCreateTime;index" json:"created_at"`
}

// TableName returns the database table name.
func (GalgameMessage) TableName() string { return "galgame_message" }
