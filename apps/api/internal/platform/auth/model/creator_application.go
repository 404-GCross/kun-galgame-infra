package model

import (
	"time"

	"gorm.io/datatypes"
)

// Creator application statuses.
const (
	CreatorAppPending  = "pending"
	CreatorAppApproved = "approved"
	CreatorAppDeclined = "declined"
)

// CreatorApplication is a user's request for the `creator` role, decided by an
// admin (apply -> review -> approve/decline, re-appliable after a cooldown).
//
// Eligibility is gated DOWNSTREAM: the forum / patch site own the policy and
// the domain data (merged PRs + published galgames come from the wiki via its
// contribution-stats API; reviews / patch resources are each site's own data),
// compute it, and only then submit the application here. This table is the
// CENTRAL queue + audit; OAuth owns the role grant (the identity contract).
// The role is single (`creator`) regardless of which site the application came
// from. See docs/auth/01-creator-role-design.md.
type CreatorApplication struct {
	ID     uint   `gorm:"primaryKey" json:"id"`
	UserID uint   `gorm:"not null;index" json:"user_id"`
	Source string `gorm:"size:16;not null" json:"source"` // forum | moyu | ...
	Status string `gorm:"size:16;not null;default:'pending'" json:"status"`
	// Evidence is the downstream-computed proof of which criterion was met
	// (e.g. {"merged_prs":7}); advisory context for the reviewing admin. The
	// admin is the hard gate — evidence is a hint, not trusted authorization.
	Evidence      datatypes.JSON `gorm:"type:jsonb" json:"evidence,omitempty"`
	Message       string         `gorm:"type:text;default:''" json:"message"`
	ReviewerID    *uint          `gorm:"column:reviewer_id" json:"reviewer_id,omitempty"`
	ReviewedAt    *time.Time     `json:"reviewed_at,omitempty"`
	DeclineReason string         `gorm:"type:text;default:''" json:"decline_reason"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func (CreatorApplication) TableName() string { return "creator_applications" }
