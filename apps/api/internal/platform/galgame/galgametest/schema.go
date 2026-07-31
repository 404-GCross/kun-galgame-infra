// Package galgametest is the single place tests provision the galgame body
// tables from.
//
// The integration suite runs `go test -p 1 ./...` against ONE shared Postgres,
// so every package that touches a galgame-family table inherits the shape
// whichever package created it first. Hand-rolled
// `CREATE TABLE IF NOT EXISTS galgame (id bigint PRIMARY KEY, ...)` stubs used
// to be harmless: some package earlier in the run always migrated the real
// models. The wave-161 retirement deleted those packages, so the first stub's
// defaultless `id` became the shape every later package inherited, and every
// model-driven INSERT failed with `null value in column "id"`.
//
// Provisioning through the real models keeps one canonical shape regardless of
// run order — including the identity default the stubs lacked.
//
// internal/platform/catalog may NOT import this package (archtest pins the
// dependency direction), so its own fixtures still spell the DDL out; they are
// bigserial-shaped to stay compatible with whatever this package migrates.
package galgametest

import (
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// EnsureBodyTables migrates the galgame body tables that more than one test
// package reads or writes. Idempotent, and safe against a database where the
// real tables already exist (AutoMigrate only adds what is missing).
//
// It deliberately does not delete anything: the tables are shared across
// packages within a run, so each caller cleans up its own fixture rows.
func EnsureBodyTables(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Galgame{},
		&model.GalgameAlias{},
		&model.GalgameCover{},
		&model.GalgameScreenshot{},
		&model.GalgameTag{},
		&model.GalgameTagRelation{},
	)
}
