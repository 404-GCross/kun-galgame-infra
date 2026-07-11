package model

import (
	"time"

	"gorm.io/datatypes"
)

// GalgameStats is the cross-source statistics snapshot table: one row per
// materialized aggregate, keyed by a stable snake_case key. The key set is the
// step-34 public-endpoint contract candidate — frozen at creation. Payload is
// the whole computed aggregate as JSON; the per-key payload shape is pinned in
// the galgamestats build package (internal/jobs/galgamestats). Rebuilt
// wholesale + idempotently by cmd/build-galgame-stats (the daily job), never
// incrementally — so serving reads a static snapshot, never the mirror.
type GalgameStats struct {
	Key string `gorm:"column:key;primaryKey" json:"key"`
	// Payload is not null with NO default tag: a meaningful empty payload is
	// still an explicit write, and the build path always sets it (GORM
	// default-tag zero trap).
	Payload datatypes.JSON `gorm:"column:payload;type:jsonb;not null" json:"payload"`
	BuiltAt time.Time      `gorm:"column:built_at;not null" json:"built_at"`
}

func (GalgameStats) TableName() string { return "galgame_stats" }
