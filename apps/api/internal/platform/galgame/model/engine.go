package model

import (
	"time"

	"gorm.io/datatypes"
)

// GalgameEngine represents a game engine
type GalgameEngine struct {
	ID          int            `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string         `gorm:"uniqueIndex;not null" json:"name"`
	Description string         `gorm:"default:''" json:"description"`
	Alias       datatypes.JSON `gorm:"type:jsonb;default:'[]'" json:"alias"`
	Created      time.Time      `gorm:"autoCreateTime" json:"created"`
	Updated      time.Time      `gorm:"autoUpdateTime" json:"updated"`
	GalgameCount int            `gorm:"column:cnt;->" json:"galgame_count"`

	Galgame []GalgameEngineRelation `gorm:"foreignKey:EngineID" json:"galgame,omitempty"`
}

func (GalgameEngine) TableName() string { return "galgame_engine" }

// GalgameEngineRelation represents a galgame↔engine association
type GalgameEngineRelation struct {
	GalgameID int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	EngineID  int       `gorm:"column:engine_id;primaryKey" json:"engine_id"`
	Created   time.Time `gorm:"autoCreateTime" json:"created"`
	Updated   time.Time `gorm:"autoUpdateTime" json:"updated"`

	Engine *GalgameEngine `gorm:"foreignKey:EngineID" json:"engine,omitempty"`
}

func (GalgameEngineRelation) TableName() string { return "galgame_engine_relation" }
