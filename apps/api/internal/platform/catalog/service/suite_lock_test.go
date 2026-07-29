package service

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// The catalog SERVICE suite's advisory lock (A2-1d follow-up to doc 106 §24).
//
// Why: these tests TRUNCATE catalog_work / catalog_work_title / catalog_release
// / catalog_external_ref and friends. So does the catalog HANDLER suite, the
// editing-engine suite and the galgameapp propose suite — and every one of them
// targets the same database whenever TEST_DATABASE_DSN is unified or `go test
// ./...` runs without -p 1. That race produced three separate "4 red, all green
// on a rerun" incidents across A2-1b and A2-1c. A suite-wide lock removes the
// class instead of re-running until it looks green.
//
// Why THIS key: 0x65647473 ("edts") is the key those sibling suites already
// hold (handler/suite_lock_test.go, editing/engine_test.go,
// galgameapp/propose_harness_test.go). Advisory keys share ONE key space across
// the whole instance, so mutual exclusion only happens between holders of the
// SAME key — minting a fresh one would have serialized this package against
// nothing. Surveyed the repo's keys before choosing: dvap (devapi), edts (the
// edit-table suites), aigw (ai), trst (trust), comm (community); the editing
// engine's per-entity business lock uses the two-int4 form with classid
// 0x65646974, which Postgres keeps in a disjoint key space.
//
// Lock ORDER is safe: galgameapp takes dvap then edts, devapi takes dvap alone,
// and nothing takes edts before dvap — so no cycle exists.
//
// A missing database changes nothing: no lock is taken and TestMain keeps its
// existing skip behavior.
const catalogSuiteLockKey int64 = 0x65647473

// acquireCatalogSuiteLock blocks until the suite lock is held and returns the
// release function (a no-op when the database is unreachable).
func acquireCatalogSuiteLock(db *gorm.DB) func() {
	if db == nil {
		return func() {}
	}
	sqlDB, err := db.DB()
	if err != nil {
		return func() {}
	}
	return lockOnDedicatedConn(sqlDB)
}

func lockOnDedicatedConn(db *sql.DB) func() {
	ctx := context.Background()
	// A session-scoped advisory lock lives on ONE connection, so it must not be
	// taken from the pool at large — a pooled connection could be handed to
	// another query and the unlock could land on a different session.
	conn, err := db.Conn(ctx)
	if err != nil {
		return func() {}
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", catalogSuiteLockKey); err != nil {
		_ = conn.Close()
		return func() {}
	}
	return func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", catalogSuiteLockKey)
		_ = conn.Close()
	}
}
