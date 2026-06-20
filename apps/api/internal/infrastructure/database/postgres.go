package database

import (
	"log"
	"os"
	"time"

	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// PostgresDB wraps gorm.DB with additional methods
type PostgresDB struct {
	db *gorm.DB
}

// NewPostgresDB creates a new PostgreSQL connection
func NewPostgresDB(cfg config.DatabaseConfig) (*PostgresDB, error) {
	gormConfig := &gorm.Config{}

	// Log slow queries + real errors, but treat ErrRecordNotFound as the normal
	// "no row" control-flow signal it is (services handle it explicitly) —
	// otherwise every first-time lookup (e.g. GET /creator/applications/me for a
	// user who never applied) logs a noisy "record not found" line.
	gormConfig.Logger = gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		},
	)

	db, err := gorm.Open(postgres.Open(cfg.DSN()), gormConfig)
	if err != nil {
		return nil, err
	}

	logger.Info("Database connected successfully")

	return &PostgresDB{db: db}, nil
}

// DB returns the underlying gorm.DB
func (p *PostgresDB) DB() *gorm.DB {
	return p.db
}

// Close closes the database connection
func (p *PostgresDB) Close() error {
	sqlDB, err := p.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// AutoMigrate runs GORM auto migration for the given models
func (p *PostgresDB) AutoMigrate(models ...any) error {
	return p.db.AutoMigrate(models...)
}
