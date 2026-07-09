// Package dbtest holds shared integration-test helpers for the community
// platform. The community test packages (migrate, service, and the Phase-B
// handler) all run against ONE Postgres database; `go test` runs separate
// package test binaries in PARALLEL by default, so their TRUNCATEs clobber each
// other's in-flight rows and a repeated run flakes. AcquireSuiteLock serializes
// those packages via a session advisory lock, making `go test
// ./internal/platform/community/...` deterministic even without -p 1 (which CI
// still pins for the whole suite).
//
// This is a regular (non-_test) package so it can be imported from the TestMain
// of each community test package; nothing in the production build imports it.
package dbtest

import (
	"context"
	"database/sql"
)

// suiteLockKey is an arbitrary constant shared by every community test package
// ("comm" as hex) — same key = mutual exclusion across the packages.
const suiteLockKey = 0x636f6d6d

// AcquireSuiteLock blocks until it holds the community test-suite advisory lock,
// returning a release func to call before the process exits. It holds the lock
// on a dedicated connection for the whole package run. A nil db or any lock
// error yields a no-op release (the suite then falls back to the -p 1
// convention, exactly as before).
func AcquireSuiteLock(db *sql.DB) func() {
	if db == nil {
		return func() {}
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return func() {}
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", suiteLockKey); err != nil {
		_ = conn.Close()
		return func() {}
	}
	return func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", suiteLockKey)
		_ = conn.Close()
	}
}
