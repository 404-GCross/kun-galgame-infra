package model

import "time"

// GalgameMigration tracks migrated galgame rows from source databases
// (kungal `galgame` or moyu `patch`). Mirrors auth/model.UserMigration in
// purpose: a stable audit trail of where each wiki galgame came from,
// keyed by the *source* id (pre-remap).
//
// Two consumers depend on this:
//
//  1. migrate-moyu-galgame: uses (source_db='moyu', source_id) as the
//     idempotency key. On re-run, an existing row means "this moyu patch
//     was already migrated — don't insert a duplicate wiki galgame for
//     it." This protects no-vndb_id patches that have no other dedup key.
//
//  2. Future migrations (e.g. avatar / banner from legacy CDN) — they
//     can reverse-look up "wiki galgame X originated from kungal id Y" /
//     "from moyu patch id Z" without having to keep the mapping in
//     external state.
//
// PK is (source_db, source_id) so a row uniquely identifies the source.
// A given wiki galgame_id can have AT MOST one record per source_db
// (i.e. one kungal record + one moyu record max), corresponding to the
// vndb_id-merge case where one wiki galgame represents the same game
// from both sites.
type GalgameMigration struct {
	SourceDB  string    `gorm:"primaryKey;column:source_db;type:varchar(20)" json:"source_db"`
	SourceID  int       `gorm:"primaryKey;column:source_id" json:"source_id"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	CreatedAt time.Time `gorm:"column:created_at;default:CURRENT_TIMESTAMP" json:"created_at"`
}

// TableName returns the table name for GalgameMigration
func (GalgameMigration) TableName() string {
	return "galgame_migrations"
}
