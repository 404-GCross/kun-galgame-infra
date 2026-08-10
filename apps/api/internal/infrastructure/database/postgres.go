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
	if err := ApplyPool(db, cfg.Pool); err != nil {
		return nil, err
	}

	logger.Info("Database connected successfully")

	return &PostgresDB{db: db}, nil
}

// OpenJob opens the handle a batch job or a cmd/ tool wants: GORM's own logging
// silenced (these write their own structured progress and a per-statement echo
// buries it) and the pool BOUNDED.
//
// It exists so that "open a database from a job" is one call with one answer.
// Every one of these sites used to inline the same gorm.Open, which meant every
// one of them inherited database/sql's unlimited MaxOpenConns — see PoolConfig
// for what that cost on 2026-08-09. A shared opener makes the bound the default
// a new job gets for free, rather than a rule someone has to remember.
//
// The DSN is passed in rather than read from config because these tools point
// at whichever database the operator names on the command line, including the
// upstream source dumps that have no config entry at all.
func OpenJob(dsn string) (*gorm.DB, error) {
	return OpenJobWith(dsn, &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
}

// OpenJobWith is OpenJob for the few callers that need a different gorm.Config
// (a louder logger while debugging, say). The pool bound is applied either way —
// that is the part no caller may opt out of by accident.
func OpenJobWith(dsn string, cfg *gorm.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, err
	}
	if err := ApplyPool(db, config.JobPoolConfig()); err != nil {
		return nil, err
	}
	return db, nil
}

// ApplyPool bounds a pool. Exported because the batch jobs open their own gorm
// handles rather than going through NewPostgresDB, and a job that runs against
// production shares the same finite server as the services do.
//
// A zero or negative bound is left at the driver default rather than silently
// substituted: an operator who sets KUN_PG_MAX_OPEN_CONNS=0 is asking for the
// unlimited behaviour, and quietly overriding that would make the escape hatch
// a lie. The defaults in config.loadPoolConfig are what keep this from being
// unbounded in practice.
func ApplyPool(db *gorm.DB, pool config.PoolConfig) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if pool.MaxOpen > 0 {
		sqlDB.SetMaxOpenConns(pool.MaxOpen)
	}
	if pool.MaxIdle > 0 {
		sqlDB.SetMaxIdleConns(pool.MaxIdle)
	}
	if pool.MaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(pool.MaxLifetime)
	}
	if pool.MaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(pool.MaxIdleTime)
	}
	return nil
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
