package model

import "time"

// MigrationProgress tracks per-object migration status so the script can
// resume across interruptions. One row per old-bucket key.
type MigrationProgress struct {
	ID          int64      `gorm:"primaryKey" json:"id"`
	Site        string     `gorm:"size:32;not null;uniqueIndex:idx_mig_site_key,priority:1" json:"site"`
	EntityType  string     `gorm:"size:32;not null" json:"entity_type"` // avatar / banner
	OldKey      string     `gorm:"size:512;not null;uniqueIndex:idx_mig_site_key,priority:2" json:"old_key"`
	NewKey      string     `gorm:"size:512" json:"new_key,omitempty"`
	Hash        string     `gorm:"type:char(64)" json:"hash,omitempty"`
	ImageID     int64      `json:"image_id,omitempty"`
	Status      string     `gorm:"size:16;not null;index" json:"status"` // pending / copied / failed / skipped
	ErrorMsg    string     `gorm:"type:text" json:"error_msg,omitempty"`
	MigratedAt  *time.Time `json:"migrated_at,omitempty"`
}

func (MigrationProgress) TableName() string {
	return "migration_progress"
}
