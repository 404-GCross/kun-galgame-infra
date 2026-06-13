package model

// GalgameOfficial represents a developer/publisher
type GalgameOfficial struct {
	ID           int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name         string    `gorm:"uniqueIndex;not null" json:"name"`
	Original     string    `gorm:"default:''" json:"original"`
	Link         string    `gorm:"default:''" json:"link"`
	Category     string    `gorm:"not null" json:"category"` // company, individual, amateur
	Lang         string    `gorm:"default:''" json:"lang"`
	Description  string    `gorm:"default:''" json:"description"`
	Created      Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated      Timestamp `gorm:"autoUpdateTime" json:"updated"`
	GalgameCount int       `gorm:"column:cnt;->;-:migration" json:"galgame_count"`

	Alias   []GalgameOfficialAlias    `gorm:"foreignKey:GalgameOfficialID" json:"alias,omitempty"`
	Galgame []GalgameOfficialRelation `gorm:"foreignKey:OfficialID" json:"galgame,omitempty"`
}

func (GalgameOfficial) TableName() string { return "galgame_official" }

// GalgameOfficialAlias represents an alias for an official
type GalgameOfficialAlias struct {
	ID                int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name              string    `gorm:"default:''" json:"name"`
	GalgameOfficialID int       `gorm:"column:galgame_official_id;not null;index" json:"galgame_official_id"`
	Created           Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated           Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameOfficialAlias) TableName() string { return "galgame_official_alias" }

// GalgameOfficialRelation represents a galgame↔official association.
//
// Source provenance mirrors GalgameTagRelation / GalgameLink: "" = user-curated,
// "vndb" = synced from VNDB. DB-column only (not in the revision Snapshot).
type GalgameOfficialRelation struct {
	GalgameID  int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	OfficialID int       `gorm:"column:official_id;primaryKey" json:"official_id"`
	Source     string    `gorm:"column:source;size:16;default:''" json:"source"`
	Created    Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated    Timestamp `gorm:"autoUpdateTime" json:"updated"`

	Official *GalgameOfficial `gorm:"foreignKey:OfficialID" json:"official,omitempty"`
}

func (GalgameOfficialRelation) TableName() string { return "galgame_official_relation" }
