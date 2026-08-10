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
