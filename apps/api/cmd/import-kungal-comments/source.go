package main

import "time"

// The source (forum) tables are EXISTING tables, so every ad-hoc struct here
// carries an explicit gorm `column` tag (charter ruling 14: GORM snake-cases
// acronyms like ID into i_d, silently mis-mapping otherwise).

// srcComment maps galgame_comment in the forum database.
type srcComment struct {
	ID              int        `gorm:"column:id"`
	Content         string     `gorm:"column:content"`
	GalgameID       int        `gorm:"column:galgame_id"`
	UserID          int        `gorm:"column:user_id"`
	ParentCommentID *int       `gorm:"column:parent_comment_id"`
	RootCommentID   *int       `gorm:"column:root_comment_id"`
	LikeCount       int        `gorm:"column:like_count"`
	Edited          *time.Time `gorm:"column:edited"`
	Status          int16      `gorm:"column:status"`
	Created         time.Time  `gorm:"column:created"`
	Updated         time.Time  `gorm:"column:updated"`
}

func (srcComment) TableName() string { return "galgame_comment" }

// srcLike maps galgame_comment_like in the forum database.
type srcLike struct {
	ID               int       `gorm:"column:id"`
	UserID           int       `gorm:"column:user_id"`
	GalgameCommentID int       `gorm:"column:galgame_comment_id"`
	Created          time.Time `gorm:"column:created"`
}

func (srcLike) TableName() string { return "galgame_comment_like" }

// commentMap maps galgame_comment_community_map — the id-mapping ledger written
// back into the SOURCE database (charter ruling 9). It is BOTH the idempotency
// key (an id already present => that comment is done) and the deep-link bridge
// that steps 03/04 read. The forum repo folds it into its own migrations later;
// this tool only creates it (reported, not adopted here).
type commentMap struct {
	OldCommentID int   `gorm:"column:old_comment_id;primaryKey"`
	ThreadID     int64 `gorm:"column:thread_id"`
	PostID       int64 `gorm:"column:post_id"`
	GalgameID    int   `gorm:"column:galgame_id"`
}

func (commentMap) TableName() string { return "galgame_comment_community_map" }

// mapTableDDL creates the mapping table idempotently in the source database.
const mapTableDDL = `
CREATE TABLE IF NOT EXISTS galgame_comment_community_map (
  old_comment_id int    PRIMARY KEY,
  thread_id      bigint NOT NULL,
  post_id        bigint NOT NULL,
  galgame_id     int    NOT NULL
)`
