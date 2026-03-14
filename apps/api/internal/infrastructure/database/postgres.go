package database

import (
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

	// Set logger based on environment
	gormConfig.Logger = gormlogger.Default.LogMode(gormlogger.Warn)

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
