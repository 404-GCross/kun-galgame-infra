package model

import "time"

// GalgameTag represents a tag definition
type GalgameTag struct {
	ID          int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	Category    string    `gorm:"not null" json:"category"` // content, sexual, technical
	Description string    `gorm:"default:''" json:"description"`
	Created     time.Time `gorm:"autoCreateTime" json:"created"`
	Updated     time.Time `gorm:"autoUpdateTime" json:"updated"`

	Alias   []GalgameTagAlias    `gorm:"foreignKey:GalgameTagID" json:"alias,omitempty"`
	Galgame []GalgameTagRelation `gorm:"foreignKey:TagID" json:"galgame,omitempty"`
}

func (GalgameTag) TableName() string { return "galgame_tag" }

// GalgameTagAlias represents an alias for a tag
type GalgameTagAlias struct {
	ID           int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string    `gorm:"default:''" json:"name"`
	GalgameTagID int       `gorm:"column:galgame_tag_id;not null;index" json:"galgame_tag_id"`
	Created      time.Time `gorm:"autoCreateTime" json:"created"`
	Updated      time.Time `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameTagAlias) TableName() string { return "galgame_tag_alias" }

// GalgameTagRelation represents a galgame↔tag association
type GalgameTagRelation struct {
	GalgameID    int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	TagID        int       `gorm:"column:tag_id;primaryKey" json:"tag_id"`
	SpoilerLevel int      `gorm:"column:spoiler_level;default:0" json:"spoiler_level"` // 0=none, 1=mild, 2=severe
	Created      time.Time `gorm:"autoCreateTime" json:"created"`
	Updated      time.Time `gorm:"autoUpdateTime" json:"updated"`

	Tag *GalgameTag `gorm:"foreignKey:TagID" json:"tag,omitempty"`
}

func (GalgameTagRelation) TableName() string { return "galgame_tag_relation" }
