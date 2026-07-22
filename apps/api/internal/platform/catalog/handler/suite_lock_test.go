package handler

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// TestMain exists solely to hold the "edts" advisory suite lock for the whole
// package run: the edit/read/admin tests TRUNCATE shared tables that the
// editing and galgameapp propose suites also write, which races whenever the
// suites target one database (unified TEST_DATABASE_DSN, or `go test ./...`
// without -p 1). The key matches those suites and is distinct from the editing
// engine's business classid 0x65646974 (two-int4 lock form) per the
// single-keyspace advisory-lock rule. A missing database changes nothing:
// no lock is taken and each test keeps its own connect-and-skip behavior.
const editSuiteLockKey int64 = 0x65647473

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	release := func() {}
	if db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: glogger.Default.LogMode(glogger.Silent)}); err == nil {
		if sqlDB, derr := db.DB(); derr == nil {
			release = acquireEditSuiteLock(sqlDB)
		}
	}
	code := m.Run()
	release()
	os.Exit(code)
}

func acquireEditSuiteLock(db *sql.DB) func() {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return func() {}
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", editSuiteLockKey); err != nil {
		_ = conn.Close()
		return func() {}
	}
	return func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", editSuiteLockKey)
		_ = conn.Close()
	}
}
