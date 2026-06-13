package model

// Column length caps for galgame_link. Keep these in sync with the gorm `size:`
// tags below AND the dto link validators — callers that build links from
// external data (e.g. VNDB curation) use them to drop values that wouldn't fit,
// rather than letting the INSERT fail. The URL cap is 500 (was 233): a few
// percent-encoded store/forum URLs run past 233, and the edit DTO already
// accepted longer values than the column held.
const (
	LinkNameMaxLen = 107
	LinkURLMaxLen  = 500
)

// GalgameLink represents an external link for a galgame
type GalgameLink struct {
	ID   int    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name string `gorm:"size:107;default:''" json:"name"`
	Link string `gorm:"size:500;default:''" json:"link"`
	// Source provenance: "" = user-added; "vndb" = auto-synced from VNDB.
	// SourceKey is the VNDB extlinks site name ("steam", "dlsite", "website",
	// "vndb", …), so the vndb-sourced subset can be reconciled idempotently
	// on re-sync without touching user-added links.
	Source    string    `gorm:"column:source;size:16;default:''" json:"source"`
	SourceKey string    `gorm:"column:source_key;size:128;default:''" json:"source_key"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated   Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameLink) TableName() string { return "galgame_link" }
