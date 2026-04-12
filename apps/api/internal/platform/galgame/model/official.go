package model

import "time"

// GalgameOfficial represents a developer/publisher
type GalgameOfficial struct {
	ID          int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"uniqueIndex;not null" json:"name"`
	Link        string    `gorm:"default:''" json:"link"`
	Category    string    `gorm:"not null" json:"category"` // company, individual, amateur
	Lang        string    `gorm:"default:''" json:"lang"`
	Description string    `gorm:"default:''" json:"description"`
	Created     time.Time `gorm:"autoCreateTime" json:"created"`
	Updated     time.Time `gorm:"autoUpdateTime" json:"updated"`

	Alias   []GalgameOfficialAlias    `gorm:"foreignKey:GalgameOfficialID" json:"alias,omitempty"`
	Galgame []GalgameOfficialRelation `gorm:"foreignKey:OfficialID" json:"galgame,omitempty"`
}

func (GalgameOfficial) TableName() string { return "galgame_official" }

// GalgameOfficialAlias represents an alias for an official
type GalgameOfficialAlias struct {
	ID                int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name              string    `gorm:"default:''" json:"name"`
	GalgameOfficialID int      `gorm:"column:galgame_official_id;not null;index" json:"galgame_official_id"`
	Created           time.Time `gorm:"autoCreateTime" json:"created"`
	Updated           time.Time `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameOfficialAlias) TableName() string { return "galgame_official_alias" }

// GalgameOfficialRelation represents a galgame↔official association
type GalgameOfficialRelation struct {
	GalgameID  int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	OfficialID int       `gorm:"column:official_id;primaryKey" json:"official_id"`
	Created    time.Time `gorm:"autoCreateTime" json:"created"`
	Updated    time.Time `gorm:"autoUpdateTime" json:"updated"`

	Official *GalgameOfficial `gorm:"foreignKey:OfficialID" json:"official,omitempty"`
}

func (GalgameOfficialRelation) TableName() string { return "galgame_official_relation" }
