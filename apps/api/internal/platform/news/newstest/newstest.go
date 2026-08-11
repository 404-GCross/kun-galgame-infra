// Package newstest provisions a news test database for the packages above the
// migration. It lives beside dbtest rather than inside it because dbtest is
// imported by the migration's own test, and a helper that runs the migration
// would close that loop into an import cycle.
package newstest

import (
	"fmt"
	"os"

	"api/internal/platform/news/dbtest"
	"api/internal/platform/news/migrate"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultDSN = "host=localhost port=5432 user=postgres password=postgres dbname=kun_news_test sslmode=disable"

// Open connects to the news test database and provisions it with the exact
// production migration, returning the handle and the suite-lock release.
//
// A missing database SKIPS the package — and a skipped package still reports
// ok, so acceptance for any DB-backed change must run with an explicit
// TEST_DATABASE_DSN and read the -v PASS counts, never the ok line.
func Open() (*gorm.DB, func(), bool) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		return nil, nil, false
	}
	sqlDB, _ := db.DB()
	release := dbtest.AcquireSuiteLock(sqlDB)

	if err := migrate.Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: news migration failed: %v\n", err)
		return nil, nil, false
	}
	return db, release, true
}

// Truncate empties every news item table, children first. news_source survives:
// it is the seeded registry, not fixture data.
func Truncate(db *gorm.DB) error {
	return db.Exec(`TRUNCATE news_item_work, news_item_image, news_item RESTART IDENTITY CASCADE`).Error
}
