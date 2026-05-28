package model

import (
	"gorm.io/datatypes"
)

// GalgamePR represents a pull request (edit request) for a galgame
type GalgamePR struct {
	ID            int            `gorm:"primaryKey;autoIncrement" json:"id"`
	GalgameID     int            `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID        int            `gorm:"column:user_id;not null;index" json:"user_id"`
	Status        int            `gorm:"default:0;check:status IN (0,1,2)" json:"status"` // 0=pending, 1=merged, 2=declined
	Note          string         `gorm:"type:text;default:''" json:"note"`
	BaseRevision  int            `gorm:"column:base_revision;not null" json:"base_revision"`
	Snapshot      datatypes.JSON `gorm:"type:jsonb;not null" json:"snapshot"`
	CompletedBy   *int           `gorm:"column:completed_by" json:"completed_by,omitempty"`
	CompletedTime *Timestamp     `gorm:"column:completed_time" json:"completed_time,omitempty"`
	RevisionID    *int           `gorm:"column:revision_id" json:"revision_id,omitempty"`
	Created       Timestamp      `gorm:"autoCreateTime" json:"created"`
	Updated       Timestamp      `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgamePR) TableName() string { return "galgame_pr" }

// GalgameRevision represents a version snapshot of a galgame
type GalgameRevision struct {
	ID        int `gorm:"primaryKey;autoIncrement" json:"id"`
	GalgameID int `gorm:"not null;uniqueIndex:idx_galgame_revision" json:"galgame_id"`
	Revision  int `gorm:"not null;uniqueIndex:idx_galgame_revision" json:"revision"`
	UserID    int `gorm:"not null;index" json:"user_id"`
	// Full action set produced by the galgame services. NOTE: GORM's
	// AutoMigrate only CREATES this CHECK on a fresh table — it never
	// ALTERs an existing one. When you add an action here you MUST also
	// bump the explicit DROP/ADD in cmd/migrate-galgame/main.go, or
	// existing wiki DBs keep the stale constraint and INSERTs 23514.
	// created/updated/merged/reverted/declined: revision_service,
	// galgame_service. claimed/edited_pending: submission_service.
	// approved/banned/unbanned/status_changed: admin_service.
	Action     string         `gorm:"size:20;not null;check:action IN ('created','updated','merged','reverted','declined','claimed','edited_pending','approved','banned','unbanned','status_changed')" json:"action"`
	Note       string         `gorm:"type:text;default:''" json:"note"`
	Snapshot   datatypes.JSON `gorm:"type:jsonb;not null" json:"snapshot"`
	IsMinor    bool           `gorm:"default:false" json:"is_minor"`
	RevertedTo *int           `gorm:"column:reverted_to" json:"reverted_to,omitempty"`
	Created    Timestamp      `gorm:"autoCreateTime" json:"created"`
}

func (GalgameRevision) TableName() string { return "galgame_revision" }

// GalgameHistory is the legacy history table (kept for migration, will be removed later)
type GalgameHistory struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Action    string    `gorm:"default:''" json:"action"`
	Type      string    `gorm:"default:''" json:"type"`
	Content   string    `gorm:"size:1007;default:''" json:"content"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated   Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameHistory) TableName() string { return "galgame_history" }
