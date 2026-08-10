package service

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

const catalogSuiteLockKey int64 = 0x65647473

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
