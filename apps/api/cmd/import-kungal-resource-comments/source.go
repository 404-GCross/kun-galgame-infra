package main

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type sourceSpec struct {
	Name         string
	Table        string
	EntityCol    string
	IsTree       bool
	CounterTable string
	CounterCol   string
	MaxRunes     int
}

var sources = []sourceSpec{
	{Name: "rating", Table: "galgame_rating_comment", EntityCol: "galgame_rating_id", IsTree: false, CounterTable: "", CounterCol: "", MaxRunes: 1314},
	{Name: "website", Table: "galgame_website_comment", EntityCol: "website_id", IsTree: true, CounterTable: "galgame_website", CounterCol: "id", MaxRunes: 5000},
	{Name: "toolset", Table: "galgame_toolset_comment", EntityCol: "toolset_id", IsTree: true, CounterTable: "", CounterCol: "", MaxRunes: 5000},
}

type srcRow struct {
	ID           int        `gorm:"column:id"`
	EntityID     int        `gorm:"column:entity_id"`
	UserID       int        `gorm:"column:user_id"`
	Content      string     `gorm:"column:content"`
	ParentID     *int       `gorm:"column:parent_id"`
	TargetUserID *int       `gorm:"column:target_user_id"`
	Edited       *time.Time `gorm:"column:edited"`
	Created      time.Time  `gorm:"column:created"`
}

func loadSource(src *gorm.DB, s sourceSpec) ([]srcRow, error) {
	var q string
	if s.IsTree {
		q = fmt.Sprintf(
			"SELECT id, %s AS entity_id, user_id, content, parent_id, "+
				"NULL::int AS target_user_id, edited, created "+
				"FROM %s ORDER BY created ASC, id ASC", s.EntityCol, s.Table)
	} else {
		q = fmt.Sprintf(
			"SELECT id, %s AS entity_id, user_id, content, "+
				"NULL::int AS parent_id, target_user_id, NULL::timestamptz AS edited, created "+
				"FROM %s ORDER BY created ASC, id ASC", s.EntityCol, s.Table)
	}
	var rows []srcRow
	if err := src.Raw(q).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load %s: %w", s.Table, err)
	}
	return rows, nil
}

type resourceMap struct {
	Source   string `gorm:"column:source;primaryKey"`
	OldID    int    `gorm:"column:old_id;primaryKey"`
	ThreadID int64  `gorm:"column:thread_id"`
	PostID   int64  `gorm:"column:post_id"`
}

func (resourceMap) TableName() string { return "resource_comment_community_map" }

const mapTableDDL = `
CREATE TABLE IF NOT EXISTS resource_comment_community_map (
  source    varchar(16) NOT NULL,
  old_id    int         NOT NULL,
  thread_id bigint      NOT NULL,
  post_id   bigint      NOT NULL,
  PRIMARY KEY (source, old_id)
)`
