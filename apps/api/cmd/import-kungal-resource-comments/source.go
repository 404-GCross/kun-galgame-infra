package main

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// sourceSpec describes one of kungal's three resource comment sections. They
// share a shape (id/content/user_id/entity_id/created) but differ in three axes
// the importer branches on: flat vs. self-referential tree, whether a display
// comment_count is reset on apply, and their anchor prefix. The prefix is also
// the map-table `source` discriminator (charter ruling 24).
type sourceSpec struct {
	Name         string // "rating" / "website" / "toolset"; also the anchor prefix + map source
	Table        string // source table in the forum database
	EntityCol    string // per-row anchor entity id column (galgame_rating_id / website_id / toolset_id)
	IsTree       bool   // website/toolset: parent_id tree; rating: pure flat
	CounterTable string // table whose comment_count is reset on apply ("" = leave untouched)
	CounterCol   string // per-row anchor entity id column in CounterTable (its own PK "id")
	MaxRunes     int    // over-length anomaly ceiling — informational only, content is never truncated
}

// sources is the fixed, ordered plan of the three sections (charter ruling
// 18/19/21). Only galgame_website carries a maintained comment_count that the
// import resets; galgame_rating.comment_count is dormant/dirty (ruling 21: not
// touched) and galgame_toolset has no such column.
var sources = []sourceSpec{
	{Name: "rating", Table: "galgame_rating_comment", EntityCol: "galgame_rating_id", IsTree: false, CounterTable: "", CounterCol: "", MaxRunes: 1314},
	{Name: "website", Table: "galgame_website_comment", EntityCol: "website_id", IsTree: true, CounterTable: "galgame_website", CounterCol: "id", MaxRunes: 5000},
	{Name: "toolset", Table: "galgame_toolset_comment", EntityCol: "toolset_id", IsTree: true, CounterTable: "", CounterCol: "", MaxRunes: 5000},
}

// srcRow is the normalized load target for every source: each section is read
// with a per-source SELECT that aliases its columns into these names, so one
// ad-hoc struct with explicit `column` tags covers all three (charter ruling
// 14: GORM snake-cases acronyms like ID into i_d, mis-mapping without tags).
// rating supplies target_user_id and NULLs parent_id/edited; the tree sources
// supply parent_id/edited and NULL target_user_id.
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

// loadSource reads one section into normalized rows, ordered (created, id) so
// the log is reproducible. The per-source SELECT is the only place the physical
// column names appear.
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

// resourceMap maps resource_comment_community_map — the id-mapping ledger
// written back into the FORUM (source) database (charter ruling 24). It is BOTH
// the idempotency key (a (source, old_id) already present => that comment is
// done) and the audit/decommission bridge for the 06a retirement. The three
// sections carry no legacy likes and no cross-thread deep links, so unlike the
// v1 galgame_comment ledger this table exists chiefly for reconciliation.
type resourceMap struct {
	Source   string `gorm:"column:source;primaryKey"`
	OldID    int    `gorm:"column:old_id;primaryKey"`
	ThreadID int64  `gorm:"column:thread_id"`
	PostID   int64  `gorm:"column:post_id"`
}

func (resourceMap) TableName() string { return "resource_comment_community_map" }

// mapTableDDL creates the shared mapping table idempotently in the source
// (forum) database. source ∈ {rating, website, toolset}; PK (source, old_id).
const mapTableDDL = `
CREATE TABLE IF NOT EXISTS resource_comment_community_map (
  source    varchar(16) NOT NULL,
  old_id    int         NOT NULL,
  thread_id bigint      NOT NULL,
  post_id   bigint      NOT NULL,
  PRIMARY KEY (source, old_id)
)`
