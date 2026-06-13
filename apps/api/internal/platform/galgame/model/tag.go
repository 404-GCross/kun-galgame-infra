package model

// GalgameTag represents a tag definition
type GalgameTag struct {
	ID           int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string    `gorm:"uniqueIndex;not null" json:"name"`
	Category     string    `gorm:"not null" json:"category"` // content, sexual, technical
	Description  string    `gorm:"default:''" json:"description"`
	Created      Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated      Timestamp `gorm:"autoUpdateTime" json:"updated"`
	GalgameCount int       `gorm:"column:cnt;->;-:migration" json:"galgame_count"`

	Alias   []GalgameTagAlias    `gorm:"foreignKey:GalgameTagID" json:"alias,omitempty"`
	Galgame []GalgameTagRelation `gorm:"foreignKey:TagID" json:"galgame,omitempty"`
}

func (GalgameTag) TableName() string { return "galgame_tag" }

// GalgameTagAlias represents an alias for a tag
type GalgameTagAlias struct {
	ID           int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string    `gorm:"default:''" json:"name"`
	GalgameTagID int       `gorm:"column:galgame_tag_id;not null;index" json:"galgame_tag_id"`
	Created      Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated      Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameTagAlias) TableName() string { return "galgame_tag_alias" }

// GalgameTagRelation represents a galgame↔tag association.
//
// Source provenance mirrors GalgameLink: "" = user-curated, "vndb" = synced
// from VNDB (the sync owns the source="vndb" subset and reconciles it
// idempotently without touching user-added tags). It lives in this column only,
// NOT in the revision Snapshot (which stays tag_ids []int): reconcileSet is
// delta-based and leaves kept rows' source untouched, so user edits preserve it.
type GalgameTagRelation struct {
	GalgameID    int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	TagID        int       `gorm:"column:tag_id;primaryKey" json:"tag_id"`
	SpoilerLevel int       `gorm:"column:spoiler_level;default:0" json:"spoiler_level"` // 0=none, 1=mild, 2=severe
	Source       string    `gorm:"column:source;size:16;default:''" json:"source"`
	Created      Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated      Timestamp `gorm:"autoUpdateTime" json:"updated"`

	Tag *GalgameTag `gorm:"foreignKey:TagID" json:"tag,omitempty"`
}

func (GalgameTagRelation) TableName() string { return "galgame_tag_relation" }
